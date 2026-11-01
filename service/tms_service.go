package service

// tms_service.go — Central TMS configuration persistence and Rule 4.
//
// UpsertCentralTmsContext saves the CentralTmsContext record and, when the
// destination name changes, executes Rule 4 (invalidate node_confirmed +
// bootstrapped tenants).

import (
	"errors"
	"fmt"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrTmsBootstrapInProgress is returned by UpsertCentralTmsContext when the
// destination name is changed while one or more tenants have TMS extension
// bootstrap running.
var ErrTmsBootstrapInProgress = errors.New("TMS extension bootstrap is in progress")

// UpsertCentralTmsContextResult holds the saved context and how many tenants
// were invalidated by Rule 4.
type UpsertCentralTmsContextResult struct {
	Context         db.CentralTmsContext
	AffectedTenants int64
}

// UpsertCentralTmsContext creates or fully replaces the CentralTmsContext.
// v1 supports a single record; subsequent calls overwrite it in place.
//
// Rule 4 (RFC 032 §5.4): if TmsApiDestinationName is changed, all tenants with
// TmsExtensionState IN (node_confirmed, bootstrapped) are invalidated to not_started
// (their subscriber TransportManagementService destination holds stale credentials
// or their node selection belongs to the old TMS system).
// If any tenant is in TmsExtensionState=bootstrapping, returns ErrTmsBootstrapInProgress.
//
// The transaction uses SELECT ... FOR UPDATE to lock all affected rows, preventing
// the race where ApplyTmsExtension's Phase 1 could transition a tenant to
// bootstrapping between the bootstrapping check and the bulk update (R4).
func (s *Service) UpsertCentralTmsContext(input db.CentralTmsContext) (*UpsertCentralTmsContextResult, error) {
	var existing db.CentralTmsContext
	err := s.DB.First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("load existing context: %w", err)
	}

	// Carry existing ID so Save does UPDATE instead of INSERT.
	if existing.ID != 0 {
		input.ID = existing.ID
	}

	destNameChanged := existing.ID != 0 && existing.TmsApiDestinationName != input.TmsApiDestinationName

	var affectedCount int64
	if destNameChanged {
		// Rule 4 requires atomicity: reject if bootstrapping, invalidate node_confirmed + bootstrapped, save context.
		// SELECT ... FOR UPDATE locks all matched rows, serialising against ApplyTmsExtension Phase 1 (R4).
		// Find loads the locked rows so we can check for bootstrapping in Go — one query to lock + read,
		// avoiding a dead-variable Count whose result is only needed for its locking side effect.
		if err := s.DB.Transaction(func(tx *gorm.DB) error {
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
					return fmt.Errorf("tenant %d has TMS extension bootstrap running; wait for completion before changing Central TMS destination: %w",
						t.ID, ErrTmsBootstrapInProgress)
				}
			}

			// Reset to not_started and clear node fields (R3: expanded to include node_confirmed).
			// Central TMS destination change may point to a different TMS system where old node
			// IDs/names map to different tenants. Only reset fields affected by Central TMS change;
			// CAS/CloudIntegration fields are unrelated and will be re-verified during re-apply.
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

			return tx.Save(&input).Error
		}); err != nil {
			return nil, err
		}
	} else {
		if err := s.DB.Save(&input).Error; err != nil {
			return nil, fmt.Errorf("save context: %w", err)
		}
	}

	return &UpsertCentralTmsContextResult{
		Context:         input,
		AffectedTenants: affectedCount,
	}, nil
}
