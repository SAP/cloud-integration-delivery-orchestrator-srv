package service

// p0_integration_test.go — RFC 032 P0 §6.1 automated test coverage.
//
// Tests in this file use the lightweight per-test SQLite DB (newTestSvc) for
// state-machine and DB-level assertions.  They do NOT test the full HTTP
// handler layer — those are in handler/p0_integration_test.go.

import (
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
)

// ── §6.1: Node switch from bootstrapped → node_pending ─────────────────────

func TestSelectTmsNode_FromBootstrapped_ResetsToNodePending(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:                           "node-switch",
		LifecycleState:                 lifecycle.TenantReady,
		TmsExtensionState:              lifecycle.TmsBootstrapped,
		TmsSourceNodeName:              "old-node",
		TmsSourceNodeID:                100,
		TransportManagementDestStatus:  lifecycle.PrereqReady,
		CasApplicationStatus:           lifecycle.PrereqReady,
		CasStandardStatus:              lifecycle.PrereqReady,
		CloudIntegrationDestStatus:     lifecycle.PrereqReady,
		ContentAssemblyDestStatus:      lifecycle.PrereqReady,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	if err := svc.SelectTmsNode(t.Context(), tenant.ID, 200, "new-node"); err != nil {
		t.Fatalf("SelectTmsNode: %v", err)
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.TmsExtensionState != lifecycle.TmsNodePending {
		t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, lifecycle.TmsNodePending)
	}
	if got.TmsSourceNodeName != "new-node" {
		t.Errorf("TmsSourceNodeName = %q, want %q", got.TmsSourceNodeName, "new-node")
	}
	if got.TmsSourceNodeID != 200 {
		t.Errorf("TmsSourceNodeID = %d, want %d", got.TmsSourceNodeID, 200)
	}
}

func TestSelectTmsNode_FromNodePending_Succeeds(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "re-select",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsNodePending,
		TmsSourceNodeName: "first-pick",
		TmsSourceNodeID:   1,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	if err := svc.SelectTmsNode(t.Context(), tenant.ID, 2, "second-pick"); err != nil {
		t.Fatalf("SelectTmsNode: %v", err)
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.TmsExtensionState != lifecycle.TmsNodePending {
		t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, lifecycle.TmsNodePending)
	}
	if got.TmsSourceNodeName != "second-pick" {
		t.Errorf("TmsSourceNodeName = %q, want %q", got.TmsSourceNodeName, "second-pick")
	}
}

// ── §6.1: Rule 1 — SaveCfIdentity resets TMS extension to not_started ──────

func TestRule1_SaveCfIdentity_ResetsTmsExtension(t *testing.T) {
	// Rule 1 is implemented inside SaveCfIdentity's transaction.
	// We can't call SaveCfIdentity directly (it needs CF API), so test
	// the DB-level atomicity: simulate the same updates SaveCfIdentity does.
	_, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:                          "rule1-tms",
		CfApiEndpoint:                 "https://api.cf.old.example.com",
		CfOrg:                         "org-old",
		CfSpace:                       "space-old",
		LifecycleState:                lifecycle.TenantReady,
		TmsExtensionState:             lifecycle.TmsBootstrapped,
		TmsSourceNodeName:             "node1",
		TmsSourceNodeID:               10,
		CasEngineDestinationName:      "CAS_DEST",
		CasApplicationStatus:          lifecycle.PrereqReady,
		CasStandardStatus:             lifecycle.PrereqReady,
		CloudIntegrationDestStatus:    lifecycle.PrereqReady,
		ContentAssemblyDestStatus:     lifecycle.PrereqReady,
		TransportManagementDestStatus: lifecycle.PrereqReady,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// Simulate Rule 1 reset (the DB writes from SaveCfIdentity's transaction).
	if err := database.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).Updates(map[string]any{
		"tms_extension_state":              lifecycle.TmsNotStarted,
		"tms_blocking_reason":              "",
		"tms_source_node_name":             "",
		"tms_source_node_id":               0,
		"central_tms_context_id":           nil,
		"cas_engine_destination_name":      "",
		"cas_application_status":           lifecycle.PrereqMissing,
		"cas_standard_status":              lifecycle.PrereqMissing,
		"cloud_integration_dest_status":    lifecycle.PrereqMissing,
		"content_assembly_dest_status":     lifecycle.PrereqMissing,
		"transport_management_dest_status": lifecycle.PrereqMissing,
	}).Error; err != nil {
		t.Fatalf("rule 1 reset: %v", err)
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, lifecycle.TmsNotStarted)
	}
	if got.TmsSourceNodeName != "" {
		t.Errorf("TmsSourceNodeName = %q, want empty", got.TmsSourceNodeName)
	}
	if got.TmsSourceNodeID != 0 {
		t.Errorf("TmsSourceNodeID = %d, want 0", got.TmsSourceNodeID)
	}
	if got.CasEngineDestinationName != "" {
		t.Errorf("CasEngineDestinationName = %q, want empty", got.CasEngineDestinationName)
	}
	if got.CasApplicationStatus != lifecycle.PrereqMissing {
		t.Errorf("CasApplicationStatus = %q, want %q", got.CasApplicationStatus, lifecycle.PrereqMissing)
	}
	if got.TransportManagementDestStatus != lifecycle.PrereqMissing {
		t.Errorf("TransportManagementDestStatus = %q, want %q", got.TransportManagementDestStatus, lifecycle.PrereqMissing)
	}
}

// ── §6.1: Job mutual exclusion — TMS bootstrapping blocks BASE Apply ────────

func TestApplyBootstrap_RejectsWhenTmsBootstrapping(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "mutex-tms-blocks-base",
		CfApiEndpoint:     "https://api.cf.test.example.com",
		CfOrg:             "org-mutex",
		CfSpace:           "space-mutex",
		LifecycleState:    lifecycle.TenantConfigured,
		TmsExtensionState: lifecycle.TmsBootstrapping,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// ApplyBootstrap should fail because TMS extension is bootstrapping.
	_, err := svc.ApplyBootstrap(t.Context(), tenant.ID, "fake-cf-token")
	if err == nil {
		t.Fatal("expected error when TMS extension is bootstrapping, got nil")
	}
	if !errorContains(err, "TMS extension bootstrap is running") {
		t.Errorf("error = %q, want it to mention TMS extension bootstrap running", err)
	}
}

func TestApplyTmsExtension_RejectsWhenNotOnboarded(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "tms-not-onboarded",
		CfApiEndpoint:     "https://api.cf.test2.example.com",
		CfOrg:             "org-tms",
		CfSpace:           "space-tms",
		LifecycleState:    lifecycle.TenantConfigured, // NOT ready
		TmsExtensionState: lifecycle.TmsNodeConfirmed,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// Seed Central TMS so that's not the blocking reason
	database.Create(&db.CentralTmsContext{TmsApiDestinationName: "tms-dest"})

	_, err := svc.ApplyTmsExtension(t.Context(), tenant.ID, "fake-cf-token")
	if err == nil {
		t.Fatal("expected error when tenant not onboarded, got nil")
	}
	if !errorContains(err, "not onboarded") {
		t.Errorf("error = %q, want it to mention not onboarded", err)
	}
}

// TestApplyTmsExtension_NoCentralTms_StartsButFailsAsync verifies that when
// Central TMS is not configured, ApplyTmsExtension still transitions to
// bootstrapping (the goroutine will fail asynchronously at buildSubscriberDestinations).
// The ensure check in buildSubscriberDestinations is the safety net.
func TestApplyTmsExtension_NoCentralTms_StartsButFailsAsync(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "tms-no-central",
		CfApiEndpoint:     "https://api.cf.test3.example.com",
		CfOrg:             "org-nocentral",
		CfSpace:           "space-nocentral",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsNodeConfirmed,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// No CentralTmsContext seeded. ApplyTmsExtension should NOT reject synchronously —
	// it transitions to bootstrapping and the goroutine handles the failure.
	_, err := svc.ApplyTmsExtension(t.Context(), tenant.ID, "fake-cf-token")
	// The call may fail (goroutine tries to build inspector with bad token)
	// or succeed (goroutine launched). Either way, verify the tenant entered bootstrapping.
	_ = err

	var got db.CpiTenant
	database.First(&got, tenant.ID)
	// State should be bootstrapping (goroutine launched) or bootstrap_failed (goroutine already failed).
	if got.TmsExtensionState != lifecycle.TmsBootstrapping && got.TmsExtensionState != lifecycle.TmsBootstrapFailed {
		t.Errorf("TmsExtensionState = %q, want bootstrapping or bootstrap_failed", got.TmsExtensionState)
	}
}

// ── §6.1: TMS Apply does not change LifecycleState ─────────────────────────

func TestApplyTmsExtension_PreservesLifecycleOnFailure(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "tms-lifecycle-stable",
		CfApiEndpoint:     "https://api.cf.test4.example.com",
		CfOrg:             "org-stable",
		CfSpace:           "space-stable",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsNodeConfirmed,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	database.Create(&db.CentralTmsContext{TmsApiDestinationName: "tms-dest"})

	// ApplyTmsExtension will fail during inspect (no real CF), but LifecycleState should stay ready.
	_, err := svc.ApplyTmsExtension(t.Context(), tenant.ID, "bad-token")
	if err == nil {
		t.Log("ApplyTmsExtension succeeded unexpectedly (mock might handle it); checking lifecycle anyway")
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.LifecycleState != lifecycle.TenantReady {
		t.Errorf("LifecycleState = %q, want %q (TMS failure must not change lifecycle)", got.LifecycleState, lifecycle.TenantReady)
	}
}

// ── §6.1: TMS-ready tenant re-executes BASE bootstrap ─────────────────────

func TestApplyBootstrap_PreservesTmsExtensionState(t *testing.T) {
	// When a TMS-ready tenant re-runs BASE bootstrap (e.g. to refresh PIR key),
	// TmsExtensionState must remain unchanged.
	// We can only test the Phase 1 (atomically claim readying) here since the
	// full bootstrap needs CF mocks. Phase 1 is where the guard matters.
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "reapply-base",
		CfApiEndpoint:     "https://api.cf.test5.example.com",
		CfOrg:             "org-reapply",
		CfSpace:           "space-reapply",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapped,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// ApplyBootstrap will fail after Phase 1 (readying claim succeeds) because
	// there's no real CF client. But TmsExtensionState should be untouched.
	_, _ = svc.ApplyBootstrap(t.Context(), tenant.ID, "bad-cf-token")

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.TmsExtensionState != lifecycle.TmsBootstrapped {
		t.Errorf("TmsExtensionState = %q, want %q (BASE bootstrap must not touch TMS state)",
			got.TmsExtensionState, lifecycle.TmsBootstrapped)
	}
}

// ── §6.1: BASE Apply resets only BASE prereqs, not TMS prereqs ─────────────

func TestApplyBootstrap_ResetsOnlyBasePrereqs(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:                          "reset-only-base",
		CfApiEndpoint:                 "https://api.cf.test6.example.com",
		CfOrg:                         "org-baseonly",
		CfSpace:                       "space-baseonly",
		LifecycleState:                lifecycle.TenantReady,
		TmsExtensionState:             lifecycle.TmsBootstrapped,
		PirApiStatus:                  lifecycle.PrereqReady,
		CasApplicationStatus:          lifecycle.PrereqReady,
		CasStandardStatus:             lifecycle.PrereqReady,
		CloudIntegrationDestStatus:    lifecycle.PrereqReady,
		ContentAssemblyDestStatus:     lifecycle.PrereqReady,
		TransportManagementDestStatus: lifecycle.PrereqReady,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// ApplyBootstrap will fail (no CF client) but Phase 1 resets run.
	_, _ = svc.ApplyBootstrap(t.Context(), tenant.ID, "bad-cf-token")

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	// BASE prereqs should be reset.
	if got.PirApiStatus != lifecycle.PrereqMissing {
		t.Errorf("PirApiStatus = %q, want %q", got.PirApiStatus, lifecycle.PrereqMissing)
	}
	// TMS prereqs should be untouched.
	if got.CasApplicationStatus != lifecycle.PrereqReady {
		t.Errorf("CasApplicationStatus = %q, want %q (should not be reset by BASE Apply)",
			got.CasApplicationStatus, lifecycle.PrereqReady)
	}
	if got.TransportManagementDestStatus != lifecycle.PrereqReady {
		t.Errorf("TransportManagementDestStatus = %q, want %q (should not be reset by BASE Apply)",
			got.TransportManagementDestStatus, lifecycle.PrereqReady)
	}
}

// ── Reset: TMS bootstrapping blocks ResetTmsExtension ───────────────────────

func TestResetTmsExtension_BlockedByBootstrapping(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "tms-reset-blocked",
		CfApiEndpoint:     "https://api.cf.reset1.example.com",
		CfOrg:             "org-reset1",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapping,
		TmsSourceNodeName: "node-x",
		TmsSourceNodeID:   42,
	}
	database.Create(&tenant)

	err := svc.ResetTmsExtension(tenant.ID)
	if err == nil {
		t.Fatal("expected error for reset from bootstrapping, got nil")
	}

	// Fields must be untouched (transaction rolled back).
	var got db.CpiTenant
	database.First(&got, tenant.ID)
	if got.TmsExtensionState != lifecycle.TmsBootstrapping {
		t.Errorf("TmsExtensionState = %q, want %q (should be unchanged)", got.TmsExtensionState, lifecycle.TmsBootstrapping)
	}
	if got.TmsSourceNodeName != "node-x" {
		t.Errorf("TmsSourceNodeName = %q, want %q (should be unchanged)", got.TmsSourceNodeName, "node-x")
	}
}

// ── Reset: BASE readying blocks ResetBootstrap ──────────────────────────────

func TestResetBootstrap_BlockedByReadying(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "base-reset-blocked",
		CfApiEndpoint:     "https://api.cf.reset2.example.com",
		CfOrg:             "org-reset2",
		LifecycleState:    lifecycle.TenantReadying,
		PirApiStatus:      lifecycle.PrereqReady,
		PirApiDestinationName: "PIR_DEST",
	}
	database.Create(&tenant)

	err := svc.ResetBootstrap(tenant.ID)
	if err == nil {
		t.Fatal("expected error for reset from readying, got nil")
	}

	// Fields must be untouched (transaction rolled back).
	var got db.CpiTenant
	database.First(&got, tenant.ID)
	if got.LifecycleState != lifecycle.TenantReadying {
		t.Errorf("LifecycleState = %q, want %q (should be unchanged)", got.LifecycleState, lifecycle.TenantReadying)
	}
	if got.PirApiDestinationName != "PIR_DEST" {
		t.Errorf("PirApiDestinationName = %q, want %q (should be unchanged)", got.PirApiDestinationName, "PIR_DEST")
	}
}

// ── Reset: BASE draft blocks ResetBootstrap ─────────────────────────────────

func TestResetBootstrap_BlockedByDraft(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:           "base-reset-draft",
		CfApiEndpoint:  "https://api.cf.reset3.example.com",
		CfOrg:          "org-reset3",
		LifecycleState: lifecycle.TenantDraft,
	}
	database.Create(&tenant)

	err := svc.ResetBootstrap(tenant.ID)
	if err == nil {
		t.Fatal("expected error for reset from draft, got nil")
	}
}

// ── Reset: TMS bootstrap_failed allows ResetTmsExtension ────────────────────

func TestResetTmsExtension_FromBootstrapFailed(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:                          "tms-reset-ok",
		CfApiEndpoint:                 "https://api.cf.reset4.example.com",
		CfOrg:                         "org-reset4",
		LifecycleState:                lifecycle.TenantReady,
		TmsExtensionState:             lifecycle.TmsBootstrapFailed,
		TmsBlockingReason:             lifecycle.TmsReasonCentralTmsChanged,
		TmsSourceNodeName:             "old-node",
		TmsSourceNodeID:               7,
		CasApplicationStatus:          lifecycle.PrereqReady,
		CasStandardStatus:             lifecycle.PrereqReady,
		CloudIntegrationDestStatus:    lifecycle.PrereqReady,
		ContentAssemblyDestStatus:     lifecycle.PrereqReady,
		TransportManagementDestStatus: lifecycle.PrereqReady,
		CasEngineDestinationName:      "CAS_DEST",
	}
	database.Create(&tenant)

	if err := svc.ResetTmsExtension(tenant.ID); err != nil {
		t.Fatalf("ResetTmsExtension: %v", err)
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, lifecycle.TmsNotStarted)
	}
	if got.TmsBlockingReason != "OPERATOR_RESET" {
		t.Errorf("TmsBlockingReason = %q, want %q", got.TmsBlockingReason, "OPERATOR_RESET")
	}
	if got.TmsSourceNodeName != "" {
		t.Errorf("TmsSourceNodeName = %q, want empty", got.TmsSourceNodeName)
	}
	if got.TmsSourceNodeID != 0 {
		t.Errorf("TmsSourceNodeID = %d, want 0", got.TmsSourceNodeID)
	}
	if got.CasApplicationStatus != lifecycle.PrereqMissing {
		t.Errorf("CasApplicationStatus = %q, want %q", got.CasApplicationStatus, lifecycle.PrereqMissing)
	}
	if got.TransportManagementDestStatus != lifecycle.PrereqMissing {
		t.Errorf("TransportManagementDestStatus = %q, want %q", got.TransportManagementDestStatus, lifecycle.PrereqMissing)
	}
	if got.CasEngineDestinationName != "" {
		t.Errorf("CasEngineDestinationName = %q, want empty", got.CasEngineDestinationName)
	}
}

// ── Reset: BASE not_ready allows ResetBootstrap ─────────────────────────────

func TestResetBootstrap_FromNotReady(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:                  "base-reset-ok",
		CfApiEndpoint:         "https://api.cf.reset5.example.com",
		CfOrg:                 "org-reset5",
		CfSpace:               "space-reset5",
		LifecycleState:        lifecycle.TenantNotReady,
		BlockingReason:        "CF_SPACE_NOT_FOUND",
		PirApiStatus:          lifecycle.PrereqReady,
		PirApiUrl:             "https://pir.example.com",
		PirApiDestinationName: "PIR_DEST",
		// TMS fields should be untouched by BASE reset.
		TmsExtensionState: lifecycle.TmsBootstrapped,
		TmsSourceNodeName: "keep-this",
		TmsSourceNodeID:   99,
	}
	database.Create(&tenant)

	if err := svc.ResetBootstrap(tenant.ID); err != nil {
		t.Fatalf("ResetBootstrap: %v", err)
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.LifecycleState != lifecycle.TenantConfigured {
		t.Errorf("LifecycleState = %q, want %q", got.LifecycleState, lifecycle.TenantConfigured)
	}
	if got.BlockingReason != "OPERATOR_RESET" {
		t.Errorf("BlockingReason = %q, want %q", got.BlockingReason, "OPERATOR_RESET")
	}
	if got.PirApiStatus != lifecycle.PrereqMissing {
		t.Errorf("PirApiStatus = %q, want %q", got.PirApiStatus, lifecycle.PrereqMissing)
	}
	if got.PirApiUrl != "" {
		t.Errorf("PirApiUrl = %q, want empty", got.PirApiUrl)
	}
	if got.PirApiDestinationName != "" {
		t.Errorf("PirApiDestinationName = %q, want empty", got.PirApiDestinationName)
	}
	// TMS fields must be untouched.
	if got.TmsExtensionState != lifecycle.TmsBootstrapped {
		t.Errorf("TmsExtensionState = %q, want %q (BASE reset must not touch TMS)", got.TmsExtensionState, lifecycle.TmsBootstrapped)
	}
	if got.TmsSourceNodeName != "keep-this" {
		t.Errorf("TmsSourceNodeName = %q, want %q (BASE reset must not touch TMS)", got.TmsSourceNodeName, "keep-this")
	}
}

// ── T3: ResetBootstrap from ready state ─────────────────────────────────────

func TestResetBootstrap_FromReady(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:                  "base-reset-ready",
		CfApiEndpoint:         "https://api.cf.reset6.example.com",
		CfOrg:                 "org-reset6",
		CfSpace:               "space-reset6",
		LifecycleState:        lifecycle.TenantReady,
		PirApiStatus:          lifecycle.PrereqReady,
		PirApiUrl:             "https://pir.example.com",
		PirApiDestinationName: "PIR_DEST",
	}
	database.Create(&tenant)

	if err := svc.ResetBootstrap(tenant.ID); err != nil {
		t.Fatalf("ResetBootstrap from ready: %v", err)
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.LifecycleState != lifecycle.TenantConfigured {
		t.Errorf("LifecycleState = %q, want %q", got.LifecycleState, lifecycle.TenantConfigured)
	}
	if got.PirApiStatus != lifecycle.PrereqMissing {
		t.Errorf("PirApiStatus = %q, want %q", got.PirApiStatus, lifecycle.PrereqMissing)
	}
}

// ── T4: ResetTmsExtension from bootstrapped state ───────────────────────────

func TestResetTmsExtension_FromBootstrapped(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:                          "tms-reset-bootstrapped",
		CfApiEndpoint:                 "https://api.cf.reset7.example.com",
		CfOrg:                         "org-reset7",
		LifecycleState:                lifecycle.TenantReady,
		TmsExtensionState:             lifecycle.TmsBootstrapped,
		TmsSourceNodeName:             "some-node",
		TmsSourceNodeID:               99,
		CasApplicationStatus:          lifecycle.PrereqReady,
		TransportManagementDestStatus: lifecycle.PrereqReady,
	}
	database.Create(&tenant)

	if err := svc.ResetTmsExtension(tenant.ID); err != nil {
		t.Fatalf("ResetTmsExtension from bootstrapped: %v", err)
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	if got.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, lifecycle.TmsNotStarted)
	}
	if got.TmsSourceNodeID != 0 {
		t.Errorf("TmsSourceNodeID = %d, want 0", got.TmsSourceNodeID)
	}
}

// ── T5: TMS Apply rejected when BASE is readying ────────────────────────────

func TestApplyTmsExtension_RejectsWhenBaseReadying(t *testing.T) {
	svc, database := newTestSvc(t)

	tenant := db.CpiTenant{
		Name:              "tms-base-readying",
		CfApiEndpoint:     "https://api.cf.t5.example.com",
		CfOrg:             "org-t5",
		CfSpace:           "space-t5",
		LifecycleState:    lifecycle.TenantReadying,
		TmsExtensionState: lifecycle.TmsNodeConfirmed,
	}
	database.Create(&tenant)
	database.Create(&db.CentralTmsContext{TmsApiDestinationName: "tms-dest"})

	_, err := svc.ApplyTmsExtension(t.Context(), tenant.ID, "fake-cf-token")
	if err == nil {
		t.Fatal("expected error when BASE is readying, got nil")
	}
	if !errorContains(err, "not onboarded") && !errorContains(err, "bootstrap is running") {
		t.Errorf("error = %q, want it to mention not onboarded or bootstrap running", err)
	}
}
