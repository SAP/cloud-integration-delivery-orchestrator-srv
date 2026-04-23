package service

import (
	"context"
	"errors"
	"fmt"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/lifecycle"

	"gorm.io/gorm"
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

// SaveCfIdentity persists the CF identity fields for the given tenant, then
// validates the supplied cfToken against the CF API.
//
// Validation checks (lightweight, read-only):
//  1. The CF space resolves to an accessible space (space GUID is valid).
//  2. The operator holds the space_developer role in that space.
//
// On success the tenant's LifecycleState is transitioned to TenantConfigured.
// On validation failure the CF fields are still saved and the state remains
// (or reverts to) TenantDraft so the operator can correct and retry.
//
// cfToken is never persisted.  It is used only within this function call to
// validate the connection, then discarded.
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

	// ── Detect key field changes ──────────────────────────────────────────────
	// If any CF identity field is changing, invalidate prior bootstrap results.
	fieldsChanged := tenant.CfApiEndpoint != input.CfApiEndpoint ||
		tenant.CfOrg != input.CfOrg ||
		tenant.CfSpace != input.CfSpace

	// ── Persist CF identity fields ───────────────────────────────────────────
	updates := map[string]any{
		"cf_api_endpoint": input.CfApiEndpoint,
		"cf_org":          input.CfOrg,
		"cf_space":        input.CfSpace,
	}
	if fieldsChanged {
		// Reset lifecycle and all prerequisite statuses so stale bootstrap
		// results are not trusted after a CF identity change.
		updates["lifecycle_state"] = lifecycle.TenantDraft
		updates["blocking_reason"] = ""
		updates["pir_api_status"] = lifecycle.PrereqMissing
		updates["cas_application_status"] = lifecycle.PrereqMissing
		updates["cas_standard_status"] = lifecycle.PrereqMissing
		updates["cloud_integration_dest_status"] = lifecycle.PrereqMissing
		updates["content_assembly_dest_status"] = lifecycle.PrereqMissing
		updates["transport_management_dest_status"] = lifecycle.PrereqMissing
		updates["tms_node_registration_status"] = lifecycle.PrereqMissing
		updates["tms_source_node_name"] = ""
		updates["central_tms_context_id"] = nil
		updates["pir_api_url"] = ""
	}
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(updates).Error; err != nil {
		return fmt.Errorf("SaveCfIdentity: persist CF fields: %w", err)
	}

	// ── Validate cfToken against CF API ──────────────────────────────────────
	// Extract operator user_id from the token (fails fast if token is malformed
	// or expired).
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

	// ── Transition to TenantConfigured ───────────────────────────────────────
	// Read the current state fresh (the updates above may have set it to draft).
	if err := s.TransitionLifecycle(tenantID, EventCfIdentityConfigured); err != nil {
		return fmt.Errorf("SaveCfIdentity: transition lifecycle: %w", err)
	}

	return nil
}
