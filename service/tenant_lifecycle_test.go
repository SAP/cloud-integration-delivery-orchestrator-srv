package service

import (
	"fmt"
	"testing"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

// TestTransitionLifecycle_ValidTransitions verifies every edge in allowedTransitions
// is accepted and writes the expected next state.
func TestTransitionLifecycle_ValidTransitions(t *testing.T) {
	cases := []struct {
		name      string
		from      lifecycle.TenantLifecycleState
		event     LifecycleEvent
		wantState lifecycle.TenantLifecycleState
	}{
		// DRAFT edges
		{"draft→configured via cf_identity_configured", lifecycle.TenantDraft, EventCfIdentityConfigured, lifecycle.TenantConfigured},
		{"draft→readying via bootstrap_started", lifecycle.TenantDraft, EventBootstrapStarted, lifecycle.TenantReadying},

		// CONFIGURED edges
		{"configured→draft via key_field_changed", lifecycle.TenantConfigured, EventKeyFieldChanged, lifecycle.TenantDraft},
		{"configured→readying via bootstrap_started", lifecycle.TenantConfigured, EventBootstrapStarted, lifecycle.TenantReadying},

		// NOT_READY edges
		{"not_ready→readying via bootstrap_started", lifecycle.TenantNotReady, EventBootstrapStarted, lifecycle.TenantReadying},
		{"not_ready→draft via key_field_changed", lifecycle.TenantNotReady, EventKeyFieldChanged, lifecycle.TenantDraft},
		{"not_ready→configured via cf_identity_configured", lifecycle.TenantNotReady, EventCfIdentityConfigured, lifecycle.TenantConfigured},

		// READYING edges
		{"readying→ready via bootstrap_finished", lifecycle.TenantReadying, EventBootstrapFinished, lifecycle.TenantReady},
		{"readying→not_ready via bootstrap_failed", lifecycle.TenantReadying, EventBootstrapFailed, lifecycle.TenantNotReady},

		// READY edges
		{"ready→draft via key_field_changed", lifecycle.TenantReady, EventKeyFieldChanged, lifecycle.TenantDraft},
		{"ready→readying via bootstrap_started", lifecycle.TenantReady, EventBootstrapStarted, lifecycle.TenantReadying},
		{"ready→configured via cf_identity_configured", lifecycle.TenantReady, EventCfIdentityConfigured, lifecycle.TenantConfigured},
	}

	svc := newTestService(nil)
	tc := newTestCleanup(t)

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tenant := db.CpiTenant{
				Name:           c.name,
				LifecycleState: c.from,
				CfOrg:          fmt.Sprintf("val-org-%d", i),
			}
			if err := testDB.Create(&tenant).Error; err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			tc.trackTenant(tenant.ID)

			if err := svc.TransitionLifecycle(tenant.ID, c.event); err != nil {
				t.Fatalf("TransitionLifecycle returned unexpected error: %v", err)
			}

			var updated db.CpiTenant
			if err := testDB.Select("lifecycle_state").First(&updated, tenant.ID).Error; err != nil {
				t.Fatalf("re-fetch tenant: %v", err)
			}
			if updated.LifecycleState != c.wantState {
				t.Errorf("state = %q, want %q", updated.LifecycleState, c.wantState)
			}
		})
	}
}

// TestTransitionLifecycle_InvalidTransitions verifies that disallowed (state, event)
// combinations are rejected with an error and leave the tenant state unchanged.
func TestTransitionLifecycle_InvalidTransitions(t *testing.T) {
	cases := []struct {
		name  string
		from  lifecycle.TenantLifecycleState
		event LifecycleEvent
	}{
		// DRAFT cannot receive bootstrap outcome events
		{"draft rejects bootstrap_finished", lifecycle.TenantDraft, EventBootstrapFinished},
		{"draft rejects bootstrap_failed", lifecycle.TenantDraft, EventBootstrapFailed},

		// CONFIGURED cannot receive bootstrap outcomes directly
		{"configured rejects bootstrap_finished", lifecycle.TenantConfigured, EventBootstrapFinished},
		{"configured rejects bootstrap_failed", lifecycle.TenantConfigured, EventBootstrapFailed},

		// NOT_READY cannot receive bootstrap outcomes (but CAN receive cf_identity_configured to re-configure)
		{"not_ready rejects bootstrap_finished", lifecycle.TenantNotReady, EventBootstrapFinished},
		{"not_ready rejects bootstrap_failed", lifecycle.TenantNotReady, EventBootstrapFailed},

		// READYING cannot receive cf_identity_configured or key_field_changed
		// (key_field_changed from readying is intentionally absent — see M3/B2)
		{"readying rejects cf_identity_configured", lifecycle.TenantReadying, EventCfIdentityConfigured},
		{"readying rejects key_field_changed", lifecycle.TenantReadying, EventKeyFieldChanged},
		{"readying rejects bootstrap_started", lifecycle.TenantReadying, EventBootstrapStarted},

		// READY cannot receive bootstrap outcomes (but CAN receive cf_identity_configured to re-configure)
		{"ready rejects bootstrap_finished", lifecycle.TenantReady, EventBootstrapFinished},
		{"ready rejects bootstrap_failed", lifecycle.TenantReady, EventBootstrapFailed},
	}

	svc := newTestService(nil)
	tc := newTestCleanup(t)

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Use a unique CfOrg per case to avoid the composite unique index on (cf_api_endpoint, cf_org).
			tenant := db.CpiTenant{
				Name:          c.name,
				LifecycleState: c.from,
				CfOrg:         fmt.Sprintf("inv-org-%d", i),
			}
			if err := testDB.Create(&tenant).Error; err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			tc.trackTenant(tenant.ID)

			err := svc.TransitionLifecycle(tenant.ID, c.event)
			if err == nil {
				t.Fatalf("expected error for (%s + %s), got nil", c.from, c.event)
			}

			// State must be unchanged
			var unchanged db.CpiTenant
			if err2 := testDB.Select("lifecycle_state").First(&unchanged, tenant.ID).Error; err2 != nil {
				t.Fatalf("re-fetch tenant: %v", err2)
			}
			if unchanged.LifecycleState != c.from {
				t.Errorf("state changed to %q after rejected transition; want %q", unchanged.LifecycleState, c.from)
			}
		})
	}
}

// TestTransitionLifecycle_TenantNotFound verifies that a non-existent tenantID
// returns an error rather than panicking.
func TestTransitionLifecycle_TenantNotFound(t *testing.T) {
	svc := newTestService(nil)
	err := svc.TransitionLifecycle(999999, EventBootstrapStarted)
	if err == nil {
		t.Fatal("expected error for non-existent tenant, got nil")
	}
}
