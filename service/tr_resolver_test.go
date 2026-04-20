package service

import (
	"context"
	"fmt"
	"testing"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cas"
	"mmt-delivery/pkg/lifecycle"
)

// =============================================================================
// tr_resolver_test.go — Unit tests for GenerateTransportRequest / exportOneTR
// =============================================================================

// trTestSetup seeds the minimal entities required for TR generation tests:
// a ready source tenant, a delivery request, and an artifact.
type trTestSetup struct {
	tc       *testCleanup
	tenant   db.CpiTenant
	dr       db.DeliveryRequest
	artifact db.Artifact
}

func setupTRTest(t *testing.T) trTestSetup {
	t.Helper()
	suffix := t.Name()
	tc := newTestCleanup(t)

	tenant := seedTenant(t, tc, "tr-src-"+suffix)
	tenant.LifecycleState = lifecycle.TenantReady
	tenant.TmsNodeRegistrationStatus = lifecycle.PrereqReady
	tenant.TmsSourceNodeName = "source-node-" + suffix
	if err := testDB.Save(&tenant).Error; err != nil {
		t.Fatalf("save tenant: %v", err)
	}

	dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "TR Test DR " + suffix,
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	artifact := seedArtifact(t, tc, db.Artifact{
		TechID:  "iflow-" + suffix,
		Version: "1.0.0",
		Name:    "Test IFlow",
		Type:    "iflow",
	})

	return trTestSetup{tc: tc, tenant: tenant, dr: dr, artifact: artifact}
}

// makeCatalog builds a minimal CAS catalog containing one package with one component.
func makeCatalog(pkgID, artifactTechID string) []cas.CatalogContentResource {
	return []cas.CatalogContentResource{
		{
			ID:         pkgID,
			ResourceID: pkgID + "-guid",
			Type:       "Cloud Integration",
			SubType:    "package",
			Name:       "Test Package",
			Version:    "1.0.0",
			Components: []cas.CatalogComponent{
				{
					ID:         artifactTechID + "-comp-guid",
					Name:       artifactTechID,
					Type:       "IFlow",
					Version:    "1.0.0",
					Exportable: true,
				},
			},
		},
	}
}

// makeFinishedCAS returns a mockCasClient pre-configured for a single FINISHED export.
func makeFinishedCAS(artifactTechID, trID string) *mockCasClient {
	return &mockCasClient{
		catalog: makeCatalog("pkg-"+artifactTechID, artifactTechID),
		exportResp: &cas.ExportResponse{
			ProcessID: "proc-" + artifactTechID,
			State:     "INITIAL",
		},
		pollStatus: &cas.OperationStatus{
			ProcessID: "proc-" + artifactTechID,
			State:     "FINISHED",
		},
		opConfig: &cas.OperationConfig{
			TransportRequestID:  trID,
			TransportRequestURL: "https://tms.example.com/tr/" + trID,
		},
	}
}

// =============================================================================
// P0 — Full success path
// =============================================================================

func TestGenerateTransportRequest_AllSucceed(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})

	casMock := makeFinishedCAS("iflow-"+suffix, "TR-0001")
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	succeeded, failed, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID, []uint{op.ID},
	)

	if fatalErr != nil {
		t.Fatalf("unexpected fatal error: %v", fatalErr)
	}
	if len(failed) != 0 {
		t.Fatalf("expected no failures, got %v", failed)
	}
	if len(succeeded) != 1 {
		t.Fatalf("expected 1 succeeded, got %d", len(succeeded))
	}
	tr, ok := succeeded[op.ID]
	if !ok {
		t.Fatal("op ID missing from succeeded map")
	}
	if tr.ID != "TR-0001" {
		t.Errorf("expected TR-0001, got %q", tr.ID)
	}

	// Verify TR number persisted to DB.
	var updated db.ArtifactTenantOperation
	if err := testDB.First(&updated, op.ID).Error; err != nil {
		t.Fatalf("reload op: %v", err)
	}
	if updated.TransportRequestNumber != "TR-0001" {
		t.Errorf("DB TransportRequestNumber=%q, want TR-0001", updated.TransportRequestNumber)
	}
}

func TestGenerateTransportRequest_AllSucceed_MultipleOps(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	// Two artifacts → two ops → two TRs.
	art2 := seedArtifact(t, s.tc, db.Artifact{
		TechID:  "iflow2-" + suffix,
		Version: "2.0.0",
		Name:    "Test IFlow 2",
		Type:    "iflow",
	})

	op1 := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})
	op2 := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        art2.ID,
		ArtifactTechID:    "iflow2-" + suffix,
		ArtifactVersion:   "2.0.0",
		TenantID:          s.tenant.ID,
	})

	// Catalog must contain both artifacts.
	catalog := append(
		makeCatalog("pkg-iflow-"+suffix, "iflow-"+suffix),
		makeCatalog("pkg-iflow2-"+suffix, "iflow2-"+suffix)...,
	)
	// Both ops share the same export process ID suffix — the mock returns a single
	// pollStatus/opConfig for all calls, so both will FINISH with TR-MULTI.
	casMock := &mockCasClient{
		catalog: catalog,
		exportResp: &cas.ExportResponse{
			ProcessID: "proc-multi",
			State:     "INITIAL",
		},
		pollStatus: &cas.OperationStatus{ProcessID: "proc-multi", State: "FINISHED"},
		opConfig: &cas.OperationConfig{
			TransportRequestID:  "TR-MULTI",
			TransportRequestURL: "https://tms.example.com/tr/TR-MULTI",
		},
	}
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	succeeded, failed, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID, []uint{op1.ID, op2.ID},
	)

	if fatalErr != nil {
		t.Fatalf("unexpected fatal error: %v", fatalErr)
	}
	if len(failed) != 0 {
		t.Fatalf("expected no failures, got %v", failed)
	}
	if len(succeeded) != 2 {
		t.Fatalf("expected 2 succeeded, got %d", len(succeeded))
	}

	// Both ops must have TR written back.
	for _, opID := range []uint{op1.ID, op2.ID} {
		var updated db.ArtifactTenantOperation
		if err := testDB.First(&updated, opID).Error; err != nil {
			t.Fatalf("reload op %d: %v", opID, err)
		}
		if updated.TransportRequestNumber != "TR-MULTI" {
			t.Errorf("op %d: DB TransportRequestNumber=%q, want TR-MULTI", opID, updated.TransportRequestNumber)
		}
	}
}

// =============================================================================
// P0 — Full failure path (artifact not in catalog)
// =============================================================================

func TestGenerateTransportRequest_AllFail_ArtifactNotInCatalog(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})

	// Catalog is empty — artifact cannot be resolved.
	casMock := &mockCasClient{
		catalog:    []cas.CatalogContentResource{},
		exportResp: &cas.ExportResponse{ProcessID: "proc-x", State: "INITIAL"},
		pollStatus: &cas.OperationStatus{ProcessID: "proc-x", State: "FINISHED"},
		opConfig:   &cas.OperationConfig{TransportRequestID: "TR-NEVER"},
	}
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	succeeded, failed, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID, []uint{op.ID},
	)

	if fatalErr != nil {
		t.Fatalf("unexpected fatal error: %v", fatalErr)
	}
	if len(succeeded) != 0 {
		t.Errorf("expected 0 succeeded, got %d", len(succeeded))
	}
	if _, ok := failed[op.ID]; !ok {
		t.Errorf("expected op %d in failed map", op.ID)
	}

	// No TR should be written back.
	var updated db.ArtifactTenantOperation
	if err := testDB.First(&updated, op.ID).Error; err != nil {
		t.Fatalf("reload op: %v", err)
	}
	if updated.TransportRequestNumber != "" {
		t.Errorf("expected empty TransportRequestNumber, got %q", updated.TransportRequestNumber)
	}
}

// =============================================================================
// P1 — Partial success path
// =============================================================================

func TestGenerateTransportRequest_PartialSuccess(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	art2 := seedArtifact(t, s.tc, db.Artifact{
		TechID:  "missing-" + suffix,
		Version: "1.0.0",
		Name:    "Missing IFlow",
		Type:    "iflow",
	})

	opOK := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})
	opFail := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        art2.ID,
		ArtifactTechID:    "missing-" + suffix, // not in catalog
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})

	// Catalog only contains the first artifact.
	casMock := &mockCasClient{
		catalog:    makeCatalog("pkg-iflow-"+suffix, "iflow-"+suffix),
		exportResp: &cas.ExportResponse{ProcessID: "proc-ok", State: "INITIAL"},
		pollStatus: &cas.OperationStatus{ProcessID: "proc-ok", State: "FINISHED"},
		opConfig:   &cas.OperationConfig{TransportRequestID: "TR-PARTIAL"},
	}
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	succeeded, failed, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID, []uint{opOK.ID, opFail.ID},
	)

	if fatalErr != nil {
		t.Fatalf("unexpected fatal error: %v", fatalErr)
	}
	if len(succeeded) != 1 {
		t.Errorf("expected 1 succeeded, got %d", len(succeeded))
	}
	if _, ok := succeeded[opOK.ID]; !ok {
		t.Errorf("expected opOK in succeeded map")
	}
	if len(failed) != 1 {
		t.Errorf("expected 1 failed, got %d", len(failed))
	}
	if _, ok := failed[opFail.ID]; !ok {
		t.Errorf("expected opFail in failed map")
	}

	// opOK must have TR written; opFail must not.
	var updatedOK db.ArtifactTenantOperation
	testDB.First(&updatedOK, opOK.ID)
	if updatedOK.TransportRequestNumber != "TR-PARTIAL" {
		t.Errorf("opOK: want TR-PARTIAL, got %q", updatedOK.TransportRequestNumber)
	}
	var updatedFail db.ArtifactTenantOperation
	testDB.First(&updatedFail, opFail.ID)
	if updatedFail.TransportRequestNumber != "" {
		t.Errorf("opFail: expected empty TransportRequestNumber, got %q", updatedFail.TransportRequestNumber)
	}
}

// =============================================================================
// P2 — Readiness gate errors (fatalErr path)
// =============================================================================

func TestGenerateTransportRequest_TenantNotReady(t *testing.T) {
	suffix := t.Name()
	tc := newTestCleanup(t)

	tenant := seedTenant(t, tc, "tr-notready-"+suffix)
	// LifecycleState is left at zero value (not ready).
	dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	svc := newTestService(nil)
	_, _, fatalErr := svc.GenerateTransportRequest(
		context.Background(), tenant.ID, dr.ID, []uint{1},
	)
	if fatalErr == nil {
		t.Fatal("expected fatal error for not-ready tenant, got nil")
	}
}

func TestGenerateTransportRequest_TmsNodeNotReady(t *testing.T) {
	suffix := t.Name()
	tc := newTestCleanup(t)

	tenant := seedTenant(t, tc, "tr-tmsnotready-"+suffix)
	tenant.LifecycleState = lifecycle.TenantReady
	// TmsNodeRegistrationStatus left at zero value (not ready).
	testDB.Save(&tenant)

	dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	svc := newTestService(nil)
	_, _, fatalErr := svc.GenerateTransportRequest(
		context.Background(), tenant.ID, dr.ID, []uint{1},
	)
	if fatalErr == nil {
		t.Fatal("expected fatal error when TMS node not ready, got nil")
	}
}

func TestGenerateTransportRequest_NoMatchingOps(t *testing.T) {
	s := setupTRTest(t)
	svc := newTestService(nil) // CAS is never reached; gate fails at step 3

	_, _, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID,
		[]uint{99999}, // non-existent op ID
	)
	if fatalErr == nil {
		t.Fatal("expected fatal error for empty op result set, got nil")
	}
}

// =============================================================================
// P2 — exportOneTR: FAILED state from CAS poll
// =============================================================================

func TestExportOneTR_PollFailed(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})

	casMock := &mockCasClient{
		catalog:    makeCatalog("pkg-iflow-"+suffix, "iflow-"+suffix),
		exportResp: &cas.ExportResponse{ProcessID: "proc-fail", State: "INITIAL"},
		pollStatus: &cas.OperationStatus{ProcessID: "proc-fail", State: "FAILED"},
	}
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	succeeded, failed, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID, []uint{op.ID},
	)

	if fatalErr != nil {
		t.Fatalf("unexpected fatal error: %v", fatalErr)
	}
	if len(succeeded) != 0 {
		t.Errorf("expected 0 succeeded, got %d", len(succeeded))
	}
	if _, ok := failed[op.ID]; !ok {
		t.Errorf("expected op %d in failed map", op.ID)
	}
}

// =============================================================================
// P2 — exportOneTR: context cancellation aborts poll
// =============================================================================

func TestExportOneTR_ContextCancellation(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})

	// Poll always returns a non-terminal state so the loop would run indefinitely.
	// We cancel the context instead.
	casMock := &mockCasClient{
		catalog:    makeCatalog("pkg-iflow-"+suffix, "iflow-"+suffix),
		exportResp: &cas.ExportResponse{ProcessID: "proc-running", State: "INITIAL"},
		pollStatus: &cas.OperationStatus{ProcessID: "proc-running", State: "RUNNING"},
	}
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before the call

	_, failed, fatalErr := svc.GenerateTransportRequest(
		ctx, s.tenant.ID, s.dr.ID, []uint{op.ID},
	)

	// acquireTenantForTR may itself fail on cancelled ctx (fatal), or the poll
	// loop catches ctx.Done and returns per-op failure.  Either way, no TR succeeds.
	if fatalErr != nil {
		return // acceptable: pre-op gate caught the cancellation
	}
	if len(failed) == 0 {
		t.Error("expected at least one failure when context is cancelled, got none")
	}
}

// =============================================================================
// P2 — exportOneTR: missing transportRequestID in config
// =============================================================================

func TestExportOneTR_MissingTransportRequestID(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})

	casMock := &mockCasClient{
		catalog:    makeCatalog("pkg-iflow-"+suffix, "iflow-"+suffix),
		exportResp: &cas.ExportResponse{ProcessID: "proc-notr", State: "INITIAL"},
		pollStatus: &cas.OperationStatus{ProcessID: "proc-notr", State: "FINISHED"},
		opConfig:   &cas.OperationConfig{TransportRequestID: ""}, // empty — invalid
	}
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	succeeded, failed, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID, []uint{op.ID},
	)

	if fatalErr != nil {
		t.Fatalf("unexpected fatal error: %v", fatalErr)
	}
	if len(succeeded) != 0 {
		t.Errorf("expected 0 succeeded, got %d", len(succeeded))
	}
	if _, ok := failed[op.ID]; !ok {
		t.Errorf("expected op in failed map due to missing TR ID")
	}
}

// =============================================================================
// Catalog error → fatal
// =============================================================================

func TestGenerateTransportRequest_CatalogError(t *testing.T) {
	s := setupTRTest(t)
	suffix := t.Name()

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: s.dr.ID,
		ArtifactID:        s.artifact.ID,
		ArtifactTechID:    "iflow-" + suffix,
		ArtifactVersion:   "1.0.0",
		TenantID:          s.tenant.ID,
	})

	casMock := &mockCasClient{
		catalogErr: fmt.Errorf("CAS unavailable"),
	}
	svc := newTestService(nil, testServiceOpts{cas: casMock})

	_, _, fatalErr := svc.GenerateTransportRequest(
		context.Background(), s.tenant.ID, s.dr.ID, []uint{op.ID},
	)
	if fatalErr == nil {
		t.Fatal("expected fatal error on catalog failure, got nil")
	}
}
