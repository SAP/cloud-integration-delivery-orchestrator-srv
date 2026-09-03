package service

import (
	"errors"
	"fmt"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
)

// allowedTransitions defines the valid (currentState, event) → nextState edges.
// Any transition not listed here is rejected by TransitionLifecycle.
var allowedTransitions = map[lifecycle.TenantLifecycleState]map[LifecycleEvent]lifecycle.TenantLifecycleState{
	lifecycle.TenantDraft: {
		// Wizard Step 1 complete: CF identity saved and token validated.
		EventCfIdentityConfigured: lifecycle.TenantConfigured,
		// EventBootstrapStarted is intentionally absent: draft means CF identity has
		// not been validated (SaveCfIdentity not yet called).  ApplyBootstrap requires
		// a validated CF identity (at least lifecycle = configured) to proceed.
	},
	lifecycle.TenantConfigured: {
		// Apply launched from wizard Step 3.
		EventBootstrapStarted: lifecycle.TenantReadying,
		// Operator re-saves CF identity while already configured (e.g. correcting
		// CfSpace after a typo).  Self-transition keeps the state at configured so
		// SaveCfIdentity no longer needs to write lifecycle_state = draft directly.
		EventCfIdentityConfigured: lifecycle.TenantConfigured,
	},
	lifecycle.TenantNotReady: {
		EventBootstrapStarted: lifecycle.TenantReadying,
		// Operator saves CF identity on a tenant that previously failed bootstrap
		// (e.g. correcting the CfSpace GUID after a CF_SPACE_NOT_FOUND failure).
		// Transitions to configured so the wizard can re-run apply.
		EventCfIdentityConfigured: lifecycle.TenantConfigured,
	},
	lifecycle.TenantReadying: {
		EventBootstrapFinished: lifecycle.TenantReady,
		EventBootstrapFailed:   lifecycle.TenantNotReady,
	},
	lifecycle.TenantReady: {
		// Operator intentionally re-applies bootstrap — e.g. to refresh service
		// key credentials or re-sync destinations after an external change.
		EventBootstrapStarted: lifecycle.TenantReadying,
		// Operator re-saves CF identity on a ready tenant (e.g. updating CfSpace
		// after a space migration).  Transitions to configured so they can re-run apply.
		EventCfIdentityConfigured: lifecycle.TenantConfigured,
	},
}

// TransitionLifecycle applies event to the tenant's LifecycleState, enforcing
// that only valid transitions are executed.
//
// It is the single authoritative entry point for all LifecycleState writes in
// the service layer.  Handlers must never write LifecycleState directly.
//
// TransitionLifecycle wraps the operation in its own DB transaction with a
// SELECT FOR UPDATE on the tenant row, so the SELECT and UPDATE are atomic
// under concurrent callers.  Callers that already hold an external transaction
// (e.g. ApplyBootstrap) should call transitionLifecycleWithDB directly.
func (s *Service) TransitionLifecycle(tenantID uint, event LifecycleEvent) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		return s.transitionLifecycleWithTx(tx, tenantID, event)
	})
}

// transitionLifecycleWithTx is the internal implementation of TransitionLifecycle
// that accepts an explicit *gorm.DB handle.  Pass a transaction handle (tx) when
// the transition must participate in a larger atomic operation — for example,
// ApplyBootstrap wraps job creation and the lifecycle transition in a single
// transaction so that no orphaned job rows can result from a partial failure.
//
// Callers that do not need a transaction should use TransitionLifecycle instead.
func (s *Service) transitionLifecycleWithTx(tx *gorm.DB, tenantID uint, event LifecycleEvent) error {
	var tenant db.CpiTenant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "lifecycle_state").First(&tenant, tenantID).Error; err != nil {
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

	if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).
		Update("lifecycle_state", nextState).Error; err != nil {
		return fmt.Errorf("TransitionLifecycle: write state %q for tenant %d: %w", nextState, tenantID, err)
	}
	return nil
}
