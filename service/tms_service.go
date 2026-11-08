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
func (s *Service) UpsertCentralTmsContext(ctx context.Context, input db.CentralTmsContext) (*UpsertCentralTmsContextResult, error) {
	// Version observed at the start of this call (outside the commit tx). The
	// commit compares the locked row against this to detect a concurrent
	// Reconfigure. 0 when no config exists yet (first-time configuration).
	var genAtStart uint64
	var startRow db.CentralTmsContext
	if err := s.DB.First(&startRow).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
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
			var existing db.CentralTmsContext
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing).Error
			firstTime := errors.Is(err, gorm.ErrRecordNotFound)
			if err != nil && !firstTime {
				return fmt.Errorf("lock existing context: %w", err)
			}

			currentGen := uint64(0)
			if !firstTime {
				currentGen = existing.Generation
				input.ID = existing.ID
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

			// Advance the authoritative version and record the validation time as
			// part of the same transaction.
			input.Generation = currentGen + 1
			input.LastValidatedAt = &validatedAt
			return tx.Save(&input).Error
		})
	}

	if _, err := s.TMS.Reconfigure(ctx, input.TmsApiDestinationName, commit); err != nil {
		return nil, fmt.Errorf("reconfigure: %w", err)
	}

	return &UpsertCentralTmsContextResult{
		Context:         input,
		AffectedTenants: affectedCount,
	}, nil
}
