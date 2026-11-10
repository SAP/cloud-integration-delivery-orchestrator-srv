package service

// tms_service.go — Central TMS configuration persistence and Reconfigure.
//
// UpsertCentralTmsContext is the single entry point for saving Central TMS
// configuration. Every call triggers a full Reconfigure: force-read destination,
// build and validate candidate TMS client, DB transaction (save + Rule 4),
// then publish the new client. This applies regardless of whether the
// destination name or content has changed (RFC 033 §6.1).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrTmsBootstrapInProgress is returned by UpsertCentralTmsContext when one or
// more tenants have TMS extension bootstrap running.
var ErrTmsBootstrapInProgress = errors.New("TMS extension bootstrap is in progress")

// singletonTmsContextID pins the CentralTmsContext to a single row. The config
// is a singleton by convention; using a fixed primary key lets the database
// reject a concurrent first-ever INSERT (two racing reconfigures on an empty
// table) via a primary-key collision instead of producing two rows.
const singletonTmsContextID = 1

// ErrTmsConfigConflict is returned when another Reconfigure advanced the
// Central TMS configuration version between the time this call read the version
// and the time it committed (optimistic concurrency conflict). The caller should
// reload and retry. Handlers map this to 409.
var ErrTmsConfigConflict = errors.New("central TMS configuration changed concurrently")

// UpsertCentralTmsContextResult holds the saved context and how many tenants
// were invalidated by Rule 4.
type UpsertCentralTmsContextResult struct {
	Context         db.CentralTmsContext
	AffectedTenants int64
}

// UpsertCentralTmsContext saves the Central TMS configuration and executes a
// full Reconfigure cycle (RFC 033 §6):
//
//  1. Force-read destination from BTP, build and validate candidate TMS client.
//  2. DB transaction (the commit callback, under the Manager lock):
//     a. Lock the CentralTmsContext row (FOR UPDATE) and compare its Generation
//        against the version observed when this call began — reject on mismatch
//        (ErrTmsConfigConflict). This serialises concurrent Reconfigures on the
//        config row and surfaces conflicts instead of silently overwriting.
//     b. Reject if any tenant is bootstrapping; execute Rule 4 (reset
//        node_confirmed + bootstrapped tenants to not_started).
//     c. Save the config with Generation incremented by 1.
//  3. The Manager swaps the active client/dest after the commit succeeds.
//
// The version column is the authoritative concurrency token shared with
// SelectTmsNode (RFC 032 L20). Every call triggers Rule 4, not just when the
// destination name changes. Validation or commit failure preserves the old
// active client and DB config.
func (s *Service) UpsertCentralTmsContext(ctx context.Context, destName string) (*UpsertCentralTmsContextResult, error) {
	// Build the row ourselves from the single caller-supplied field. The handler
	// no longer binds the request straight onto db.CentralTmsContext, so a client
	// cannot set ID / CreatedAt / DeletedAt (L3).
	input := db.CentralTmsContext{TmsApiDestinationName: destName}

	// Version observed at the start of this call (outside the commit tx). The
	// commit compares the locked row against this to detect a concurrent
	// Reconfigure. 0 when no config exists yet (first-time configuration).
	// Unscoped so a soft-deleted singleton row is still seen (defensive).
	var genAtStart uint64
	var startRow db.CentralTmsContext
	if err := s.DB.Unscoped().First(&startRow).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("read current central TMS version: %w", err)
	}
	genAtStart = startRow.Generation

	var affectedCount int64

	// commit runs under the Manager lock after the candidate is validated. It owns
	// the DB transaction: version compare-and-set + Rule 4 + save. validatedAt is
	// the timestamp of the candidate's successful GetNodes validation; it is
	// persisted so DB and the manager snapshot share one last-validated time.
	commit := func(_ cf.Destination, validatedAt time.Time) error {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			// Lock the single config row (if any) and re-check the version.
			// The config is a singleton pinned to a fixed primary key, so a
			// first-ever INSERT by two racing reconfigures collides on the PK
			// (FOR UPDATE cannot lock a not-yet-existing row); the loser's Save
			// fails with a duplicate-key error, which we map to a conflict.
			var existing db.CentralTmsContext
			err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing).Error
			firstTime := errors.Is(err, gorm.ErrRecordNotFound)
			if err != nil && !firstTime {
				return fmt.Errorf("lock existing context: %w", err)
			}

			currentGen := uint64(0)
			if !firstTime {
				currentGen = existing.Generation
			}
			if tmsConfigChanged(genAtStart, currentGen) {
				return fmt.Errorf("%w: version %d → %d", ErrTmsConfigConflict, genAtStart, currentGen)
			}

			// Lock tenants that might be affected by Rule 4 or are currently bootstrapping.
			// SELECT ... FOR UPDATE serialises against ApplyTmsExtension Phase 1 (R4-A).
			// Lock order: config row first (above), then tenant rows (here) — shared
			// with SelectTmsNode to avoid deadlock.
			var tenants []db.CpiTenant
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Select("id", "tms_extension_state").
				Where("tms_extension_state IN ?", []lifecycle.TmsExtensionState{
					lifecycle.TmsBootstrapping, lifecycle.TmsNodeConfirmed, lifecycle.TmsBootstrapped,
				}).
				Find(&tenants).Error; err != nil {
				return fmt.Errorf("rule 4: lock tenants: %w", err)
			}

			for _, t := range tenants {
				if t.TmsExtensionState == lifecycle.TmsBootstrapping {
					return fmt.Errorf("tenant %d has TMS extension bootstrap running; wait for completion before reconfiguring Central TMS: %w",
						t.ID, ErrTmsBootstrapInProgress)
				}
			}

			// Rule 4: reset node_confirmed + bootstrapped tenants to not_started.
			result := tx.Model(&db.CpiTenant{}).
				Where("tms_extension_state IN ?", []lifecycle.TmsExtensionState{
					lifecycle.TmsNodeConfirmed, lifecycle.TmsBootstrapped,
				}).
				Updates(map[string]any{
					"tms_extension_state":              lifecycle.TmsNotStarted,
					"tms_blocking_reason":              lifecycle.TmsReasonCentralTmsChanged,
					"tms_source_node_name":             "",
					"tms_source_node_id":               0,
					"transport_management_dest_status": lifecycle.PrereqMissing,
				})
			if result.Error != nil {
				return fmt.Errorf("rule 4: invalidate tenants: %w", result.Error)
			}
			affectedCount = result.RowsAffected

			// First-ever config (no row to lock, version compare is 0==0 for every
			// racer): the ONLY guard against a concurrent first reconfigure is the
			// primary key. Use Create, not Save: Create is a pure INSERT, so a racer
			// that already committed makes this collide on the fixed singleton key
			// and fail (rolling back). Save's upsert semantics would instead find
			// the just-inserted row, turn into an UPDATE, and silently overwrite it
			// — reintroducing the lost-update bug this fix closes. The PK-collision
			// error is mapped to ErrTmsConfigConflict so the loser gets a 409, not
			// a raw 500 (same contract as the steady-state version conflict).
			if firstTime {
				input.ID = singletonTmsContextID
				input.Generation = currentGen + 1
				input.LastValidatedAt = &validatedAt
				if err := tx.Create(&input).Error; err != nil {
					if db.IsUniqueViolation(err) {
						return fmt.Errorf("%w: concurrent first-time configuration", ErrTmsConfigConflict)
					}
					return err
				}
				return nil
			}

			// Steady state (row exists): the FOR UPDATE lock + version compare above
			// already serialise racers. Update only the columns we own — never a
			// full-row Save, which would zero created_at and let a client-supplied
			// DeletedAt through (L3). Unscoped + deleted_at=NULL defensively revives
			// a row that was somehow soft-deleted.
			input.ID = existing.ID
			input.Generation = currentGen + 1
			input.LastValidatedAt = &validatedAt
			return tx.Unscoped().Model(&db.CentralTmsContext{}).Where("id = ?", existing.ID).
				Updates(map[string]any{
					"tms_api_destination_name": input.TmsApiDestinationName,
					"generation":               input.Generation,
					"last_validated_at":        input.LastValidatedAt,
					"deleted_at":               nil,
				}).Error
		})
	}

	if _, err := s.TMS.Reconfigure(ctx, destName, commit); err != nil {
		return nil, fmt.Errorf("reconfigure: %w", err)
	}

	return &UpsertCentralTmsContextResult{
		Context:         input,
		AffectedTenants: affectedCount,
	}, nil
}
