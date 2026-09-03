package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CfIdentityInput carries the CF identity fields that Wizard Step 1 persists.
// Only the fields listed here are written; all other CpiTenant fields are
// untouched by SaveCfIdentity.
type CfIdentityInput struct {
	// CfApiEndpoint is the CF API root URL, e.g. "https://api.cf.eu10.hana.ondemand.com".
	CfApiEndpoint string `json:"cfApiEndpoint" binding:"required"`

	// CfOrg is the CF organisation GUID for the subscriber's subaccount.
	CfOrg string `json:"cfOrg" binding:"required"`

	// CfSpace is the CF space GUID where service instances are created.
	CfSpace string `json:"cfSpace" binding:"required"`
}

// SaveCfIdentity is the backend entry point for Wizard Step 1 "Start Bootstrap" /
// "Re-bootstrap". It performs three actions unconditionally:
//
//  1. Validates the provided cfToken against the CF API
//     (space accessible + operator holds space_developer role).
//  2. Atomically resets ALL bootstrap-related state fields and transitions
//     LifecycleState to CONFIGURED in a single SELECT FOR UPDATE transaction.
//     The readying guard prevents SaveCfIdentity from corrupting a bootstrap
//     goroutine that is already running.  Combining reset and transition in one
//     transaction eliminates any window between the two writes.
//
// cfToken is never persisted; it is used only within this call and then discarded.
func (s *Service) SaveCfIdentity(ctx context.Context, tenantID uint, input CfIdentityInput, cfToken string) error {
	// ── Load tenant ──────────────────────────────────────────────────────────
	var tenant db.CpiTenant
	if err := s.DB.First(&tenant, tenantID).Error; err != nil {
		return fmt.Errorf("SaveCfIdentity: fetch tenant %d: %w", tenantID, err)
	}

	// ── Reject duplicate (CfApiEndpoint, CfOrg) if changing to a pair already in use ──
	if input.CfApiEndpoint != tenant.CfApiEndpoint || input.CfOrg != tenant.CfOrg {
		var conflict db.CpiTenant
		err := s.DB.Where("cf_api_endpoint = ? AND cf_org = ? AND id != ?", input.CfApiEndpoint, input.CfOrg, tenantID).
			First(&conflict).Error
		if err == nil {
			return fmt.Errorf("SaveCfIdentity: CF org %q on %q is already registered as another CPI tenant (id=%d)", input.CfOrg, input.CfApiEndpoint, conflict.ID)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("SaveCfIdentity: check duplicate CF identity: %w", err)
		}
	}

	// ── Validate cfToken against CF API ──────────────────────────────────────
	// Validation runs before any state reset: a bad token must not downgrade
	// an otherwise healthy tenant.
	userID, err := cf.ExtractUserID(cfToken)
	if err != nil {
		return fmt.Errorf("SaveCfIdentity: extract user_id from cfToken: %w", err)
	}

	cfcl, err := cf.NewCFClient(input.CfApiEndpoint, cfToken)
	if err != nil {
		return fmt.Errorf("SaveCfIdentity: create CF client: %w", err)
	}

	// Check 1: space is accessible.
	space, err := cfcl.GetSpace(ctx, input.CfSpace)
	if err != nil || space == nil {
		return fmt.Errorf("SaveCfIdentity: CF space %q is not accessible with the provided token: %w",
			input.CfSpace, err)
	}

	// Check 2: operator holds space_developer role.
	hasDev, err := cfcl.HasSpaceDeveloperRole(ctx, input.CfSpace, userID)
	if err != nil {
		return fmt.Errorf("SaveCfIdentity: role check: %w", err)
	}
	if !hasDev {
		return fmt.Errorf("SaveCfIdentity: operator does not hold space_developer role in space %q", input.CfSpace)
	}

	// ── Atomic guard + full state reset + lifecycle transition ───────────────
	// All three writes share one SELECT FOR UPDATE transaction:
	//   1. Guard: reject if lifecycle = readying (bootstrap already running).
	//   2. Reset all prereq and CF identity fields.
	//   3. Transition lifecycle → configured via transitionLifecycleWithTx.
	// Combining reset and transition in one transaction eliminates the window
	// where a concurrent ApplyBootstrap could observe the post-reset state
	// before lifecycle reaches configured.
	// lifecycle_state is intentionally excluded from updates: it is written
	// exclusively by transitionLifecycleWithTx via the allowedTransitions table.
	updates := map[string]any{
		"cf_api_endpoint":                  input.CfApiEndpoint,
		"cf_org":                           input.CfOrg,
		"cf_space":                         input.CfSpace,
		"blocking_reason":                  "",
		"pir_api_status":                   lifecycle.PrereqMissing,
		"cas_application_status":           lifecycle.PrereqMissing,
		"cas_standard_status":              lifecycle.PrereqMissing,
		"cloud_integration_dest_status":    lifecycle.PrereqMissing,
		"content_assembly_dest_status":     lifecycle.PrereqMissing,
		"transport_management_dest_status": lifecycle.PrereqMissing,
		"tms_node_registration_status":     lifecycle.PrereqMissing,
		"tms_source_node_name":             "",
		"tms_source_node_id":               0,
		"central_tms_context_id":           nil,
		"pir_api_url":                      "",
		"pir_api_destination_name":         "",
		"cas_engine_destination_name":      "",
	}
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t db.CpiTenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "lifecycle_state").First(&t, tenantID).Error; err != nil {
			return err
		}
		if t.LifecycleState == lifecycle.TenantReadying {
			return fmt.Errorf("bootstrap is currently running; wait for it to complete or call ResetBootstrap first")
		}
		if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(updates).Error; err != nil {
			return err
		}
		return s.transitionLifecycleWithTx(tx, tenantID, EventCfIdentityConfigured)
	}); err != nil {
		return fmt.Errorf("SaveCfIdentity: reset and configure: %w", err)
	}

	return nil
}
