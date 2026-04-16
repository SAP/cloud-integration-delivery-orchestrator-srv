package service

import (
	"errors"
	"fmt"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

// ErrTransitionNotAllowed is returned by TransitionLifecycle when the
// requested event is not a valid transition from the tenant's current state.
// Callers can use errors.Is to distinguish this from infrastructure errors.
var ErrTransitionNotAllowed = errors.New("transition not allowed")

// LifecycleEvent represents a trigger that causes a TenantLifecycleState transition.
type LifecycleEvent string

const (
	// EventCfIdentityConfigured is fired by SaveCfIdentity when the CF identity
	// fields have been saved and the cfToken has been validated against the CF API
	// (space accessible + operator has Space Developer role).
	// Transitions draft → configured.
	EventCfIdentityConfigured LifecycleEvent = "cf_identity_configured"

	// EventBootstrapStarted is fired when an apply or retry job begins execution.
	EventBootstrapStarted LifecycleEvent = "bootstrap_started"

	// EventBootstrapFinished is fired when a bootstrap job completes all steps
	// successfully.
	EventBootstrapFinished LifecycleEvent = "bootstrap_finished"

	// EventBootstrapFailed is fired when a bootstrap job reaches a terminal
	// non-finished state (failed, waiting_user_action, partially_applied).
	EventBootstrapFailed LifecycleEvent = "bootstrap_failed"

	// EventKeyFieldChanged is fired by the update handler when any of the core
	// CF identity fields change (CfApiEndpoint, CfOrg, CfSpace).
	// Any bootstrap result is now stale.
	EventKeyFieldChanged LifecycleEvent = "key_field_changed"
)

// allowedTransitions defines the valid (currentState, event) → nextState edges.
// Any transition not listed here is rejected by TransitionLifecycle.
var allowedTransitions = map[lifecycle.TenantLifecycleState]map[LifecycleEvent]lifecycle.TenantLifecycleState{
	lifecycle.TenantDraft: {
		// Wizard Step 1 complete: CF identity saved and token validated.
		EventCfIdentityConfigured: lifecycle.TenantConfigured,
		// Apply launched directly from draft (e.g. re-apply after key field change
		// in a tool-driven flow where the caller skips the wizard).
		EventBootstrapStarted: lifecycle.TenantReadying,
	},
	lifecycle.TenantConfigured: {
		// CF identity fields changed after Step 1 — must re-validate.
		EventKeyFieldChanged: lifecycle.TenantDraft,
		// Apply launched from wizard Step 3.
		EventBootstrapStarted: lifecycle.TenantReadying,
	},
	lifecycle.TenantNotReady: {
		EventBootstrapStarted: lifecycle.TenantReadying,
		EventKeyFieldChanged:  lifecycle.TenantDraft,
	},
	lifecycle.TenantReadying: {
		EventBootstrapFinished: lifecycle.TenantReady,
		EventBootstrapFailed:   lifecycle.TenantNotReady,
	},
	lifecycle.TenantReady: {
		EventKeyFieldChanged: lifecycle.TenantDraft,
		// Re-apply is allowed from ready (e.g. credential rotation).
		EventBootstrapStarted: lifecycle.TenantReadying,
	},
}

// TransitionLifecycle applies event to the tenant's LifecycleState, enforcing
// that only valid transitions are executed.
//
// It is the single authoritative entry point for all LifecycleState writes in
// the service layer.  Handlers must never write LifecycleState directly.
func (s *Service) TransitionLifecycle(tenantID uint, event LifecycleEvent) error {
	var tenant db.CpiTenant
	if err := s.DB.Select("id", "lifecycle_state").First(&tenant, tenantID).Error; err != nil {
		return fmt.Errorf("TransitionLifecycle: fetch tenant %d: %w", tenantID, err)
	}

	events, ok := allowedTransitions[tenant.LifecycleState]
	if !ok {
		return fmt.Errorf("TransitionLifecycle: no transitions defined for state %q", tenant.LifecycleState)
	}

	nextState, ok := events[event]
	if !ok {
		return fmt.Errorf("TransitionLifecycle: event %q is not valid from state %q (tenant %d): %w",
			event, tenant.LifecycleState, tenantID, ErrTransitionNotAllowed)
	}

	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).
		Update("lifecycle_state", nextState).Error; err != nil {
		return fmt.Errorf("TransitionLifecycle: write state %q for tenant %d: %w", nextState, tenantID, err)
	}
	return nil
}
