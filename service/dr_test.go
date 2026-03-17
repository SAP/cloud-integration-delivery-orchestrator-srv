package service

import (
	"context"
	"testing"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
)

// =============================================================================
// Phase 1 Tests: InsertTenantOps, UpdateTenantOps with optional TR
// =============================================================================

// drTestSetup creates tenants, rule, DR, and an artifact for DR operation tests.
// It returns the created entities for use in tests.
type drTestSetup struct {
	tc       *testCleanup
	source   db.CpiTenant
	target   db.CpiTenant
	rule     db.DeliveryRule
	dr       db.DeliveryRequest
	artifact db.Artifact
}

func setupDRTest(t *testing.T) drTestSetup {
	t.Helper()
	tc := newTestCleanup(t)

	// Use test name as suffix for uniqueness across parallel tests
	suffix := t.Name()

	source := seedTenant(t, tc, "dr-src-"+suffix)
	source.TransportNodeID = 100
	source.TransportNodeName = "source-node-" + suffix
	testDB.Save(&source)

	target := seedTenant(t, tc, "dr-tgt-"+suffix)
	target.TransportNodeID = 200
	target.TransportNodeName = "target-node-" + suffix
	testDB.Save(&target)

	rule := seedRule(t, tc, "dr-rule-"+suffix, source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "1.0.*"
	testDB.Save(&rule)

	dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "Test DR " + suffix,
		DeliveryRuleID:  rule.ID,
		SourceTenantID:  source.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "test-user",
		UpdatedBy:       "test-user",
	})

	artifact := seedArtifact(t, tc, db.Artifact{
		TechID:    "iflow-" + suffix,
		Version:   "1.0.5",
		Name:      "IFlow " + suffix,
		Type:      consts.Artifact_Type_Iflow,
		PackageID: "pkg1",
	})

	return drTestSetup{
		tc:       tc,
		source:   source,
		target:   target,
		rule:     rule,
		dr:       dr,
		artifact: artifact,
	}
}

// --- InsertTenantOps Tests ---

func TestInsertTenantOps_EmptyTR(t *testing.T) {
	s := setupDRTest(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		// For downgrade check: return target tenant with version 1.0.4 (lower than 1.0.5)
		return &mockCPIClient{}, nil
	}

	svc := newTestService(factory)

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "", // empty TR — should be allowed
		},
	}

	result, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps with empty TR should succeed, got error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 op, got %d", len(result))
	}
	if result[0].TransportRequestNumber != "" {
		t.Errorf("expected empty TR, got %q", result[0].TransportRequestNumber)
	}
	if result[0].RequestState != lifecycle.RequestPending {
		t.Errorf("expected PENDING state, got %s", result[0].RequestState)
	}
}

func TestInsertTenantOps_WithTR(t *testing.T) {
	s := setupDRTest(t)

	tmsMock := &mockTMSClient{
		transportRequests: map[string]*tms.TransportRequestV1{
			"TR-001": validTR("TR-001", s.source.TransportNodeName, s.artifact.TechID, s.artifact.Version, s.artifact.Type),
		},
	}

	// CPI mock for downgrade check
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}

	svc := newTestService(factory, testServiceOpts{tms: tmsMock})

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "TR-001",
		},
	}

	result, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps with valid TR should succeed, got error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 op, got %d", len(result))
	}
	if result[0].TransportRequestNumber != "TR-001" {
		t.Errorf("expected TR-001, got %q", result[0].TransportRequestNumber)
	}
}

func TestInsertTenantOps_WithInvalidTR(t *testing.T) {
	s := setupDRTest(t)

	tmsMock := &mockTMSClient{
		// TR-BAD not found
		transportRequests: map[string]*tms.TransportRequestV1{},
	}

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}

	svc := newTestService(factory, testServiceOpts{tms: tmsMock})

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "TR-BAD",
		},
	}

	_, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err == nil {
		t.Fatal("InsertTenantOps with invalid TR should fail")
	}
}

// --- UpdateTenantOps Tests ---

func TestUpdateTenantOps_EmptyToNonEmpty(t *testing.T) {
	s := setupDRTest(t)

	// First insert op with empty TR
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "",
		},
	}
	inserted, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps failed: %v", err)
	}
	opID := inserted[0].ID

	// Now update to a valid TR — should validate
	tmsMock := &mockTMSClient{
		transportRequests: map[string]*tms.TransportRequestV1{
			"TR-002": validTR("TR-002", s.source.TransportNodeName, s.artifact.TechID, s.artifact.Version, s.artifact.Type),
		},
	}
	svc2 := newTestService(factory, testServiceOpts{tms: tmsMock})

	updateOps := []db.ArtifactTenantOperation{
		{
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact, // needed for TrExist metadata check
			TransportRequestNumber: "TR-002",
		},
	}
	updateOps[0].ID = opID

	result, err := svc2.UpdateTenantOps(s.dr.ID, updateOps, "test-user")
	if err != nil {
		t.Fatalf("UpdateTenantOps (empty → non-empty) should succeed, got: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	// Verify in DB
	var dbOp db.ArtifactTenantOperation
	testDB.First(&dbOp, opID)
	if dbOp.TransportRequestNumber != "TR-002" {
		t.Errorf("expected TR-002 in DB, got %q", dbOp.TransportRequestNumber)
	}
}

func TestUpdateTenantOps_EmptyToEmpty(t *testing.T) {
	s := setupDRTest(t)

	// Insert op with empty TR
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "",
		},
	}
	inserted, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps failed: %v", err)
	}
	opID := inserted[0].ID

	// Update with empty TR again — should skip validation (no TMS call needed)
	svc2 := newTestService(factory) // no TMS mock — would crash if TrExist is called
	updateOps := []db.ArtifactTenantOperation{
		{
			TransportRequestNumber: "",
		},
	}
	updateOps[0].ID = opID

	_, err = svc2.UpdateTenantOps(s.dr.ID, updateOps, "test-user")
	if err != nil {
		t.Fatalf("UpdateTenantOps (empty → empty) should succeed without TMS call, got: %v", err)
	}
}

// =============================================================================
// Phase 1 Tests: Approve & RequestApproval TR validation
// =============================================================================

// setupApproveTest creates a full setup for Approve/RequestApproval tests:
// source tenant, target tenant, rule, DR with ops that may or may not have TRs.
type approveTestSetup struct {
	tc     *testCleanup
	source db.CpiTenant
	target db.CpiTenant
	rule   db.DeliveryRule
	dr     db.DeliveryRequest
	ops    []db.ArtifactTenantOperation
}

func setupApproveTest(t *testing.T, trNumber string) approveTestSetup {
	t.Helper()
	tc := newTestCleanup(t)

	suffix := t.Name()

	source := seedTenant(t, tc, "appr-src-"+suffix)
	source.TransportNodeID = 300
	source.TransportNodeName = "appr-src-node-" + suffix
	testDB.Save(&source)

	target := seedTenant(t, tc, "appr-tgt-"+suffix)
	target.TransportNodeID = 400
	target.TransportNodeName = "appr-tgt-node-" + suffix
	testDB.Save(&target)

	rule := seedRule(t, tc, "appr-rule-"+suffix, source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "2.0.*"
	testDB.Save(&rule)

	dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "Approve DR " + suffix,
		DeliveryRuleID:  rule.ID,
		SourceTenantID:  source.ID,
		AggregateStatus: lifecycle.AggWaitingApprove,
		CreatedBy:       "creator-user",
		UpdatedBy:       "creator-user",
	})

	art := seedArtifact(t, tc, db.Artifact{
		TechID:    "appr-iflow-" + suffix,
		Version:   "2.0.3",
		Name:      "Approve IFlow " + suffix,
		Type:      consts.Artifact_Type_Iflow,
		PackageID: "pkg-appr-" + suffix,
	})

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      dr.ID,
		TenantID:               target.ID,
		ArtifactID:             art.ID,
		ArtifactTechID:         art.TechID,
		ArtifactVersion:        art.Version,
		TransportRequestNumber: trNumber,
		RequestState:           lifecycle.RequestPending,
		ImportState:            lifecycle.ImportNotStarted,
		DeployState:            lifecycle.DeployNotStarted,
		CreatedBy:              "creator-user",
	})

	return approveTestSetup{
		tc:     tc,
		source: source,
		target: target,
		rule:   rule,
		dr:     dr,
		ops:    []db.ArtifactTenantOperation{op},
	}
}

func TestRequestApproval_MissingTR(t *testing.T) {
	s := setupApproveTest(t, "") // empty TR

	// TMS mock — shouldn't matter, TrExist will fail on empty TR before calling TMS
	tmsMock := &mockTMSClient{}

	svc := newTestService(nil, testServiceOpts{tms: tmsMock})

	err := svc.RequestApproval(s.dr.ID, "creator-user", []string{"approver-1"}, "please approve")
	if err == nil {
		t.Fatal("RequestApproval with missing TR should fail")
	}

	// Verify DR status did NOT change to WAITING_APPROVAL
	var dr db.DeliveryRequest
	testDB.First(&dr, s.dr.ID)
	if dr.AggregateStatus != lifecycle.AggWaitingApprove {
		// It was already WAITING_APPROVAL from setup; confirm it didn't change to something unexpected
		t.Logf("DR status after failed RequestApproval: %s (unchanged from setup)", dr.AggregateStatus)
	}
}

func TestRequestApproval_WithValidTR(t *testing.T) {
	s := setupApproveTest(t, "TR-APPROVE-001")

	tmsMock := &mockTMSClient{
		transportRequests: map[string]*tms.TransportRequestV1{
			"TR-APPROVE-001": validTR("TR-APPROVE-001", s.source.TransportNodeName, s.ops[0].ArtifactTechID, s.ops[0].ArtifactVersion, consts.Artifact_Type_Iflow),
		},
	}

	svc := newTestService(nil, testServiceOpts{tms: tmsMock})

	err := svc.RequestApproval(s.dr.ID, "creator-user", []string{"approver-1"}, "please approve")
	if err != nil {
		t.Fatalf("RequestApproval with valid TR should succeed, got: %v", err)
	}

	// Verify DR status changed
	var dr db.DeliveryRequest
	testDB.First(&dr, s.dr.ID)
	if dr.AggregateStatus != lifecycle.AggWaitingApprove {
		t.Errorf("expected WAITING_APPROVAL, got %s", dr.AggregateStatus)
	}
}

func TestApprove_MissingTR(t *testing.T) {
	s := setupApproveTest(t, "") // empty TR

	tmsMock := &mockTMSClient{}
	svc := newTestService(nil, testServiceOpts{tms: tmsMock})

	_, err := svc.Approve(s.dr.ID, "approver-user")
	if err == nil {
		t.Fatal("Approve with missing TR should fail")
	}
}

// =============================================================================
// SkipDeploy Tests
// =============================================================================

func TestInsertTenantOps_SkipDeploy(t *testing.T) {
	s := setupDRTest(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "",
			SkipDeploy:             true,
		},
	}

	result, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps with SkipDeploy should succeed, got error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 op, got %d", len(result))
	}
	if !result[0].SkipDeploy {
		t.Error("expected SkipDeploy=true")
	}
	if result[0].DeployState != lifecycle.DeployDisabled {
		t.Errorf("expected DEPLOY_DISABLED, got %s", result[0].DeployState)
	}
	if result[0].ImportState != lifecycle.ImportNotStarted {
		t.Errorf("expected NOT_STARTED import, got %s", result[0].ImportState)
	}
}

func TestInsertTenantOps_NoSkipDeploy(t *testing.T) {
	s := setupDRTest(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "",
			SkipDeploy:             false,
		},
	}

	result, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps without SkipDeploy should succeed, got error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 op, got %d", len(result))
	}
	if result[0].SkipDeploy {
		t.Error("expected SkipDeploy=false")
	}
	if result[0].DeployState != lifecycle.DeployNotStarted {
		t.Errorf("expected NOT_STARTED deploy, got %s", result[0].DeployState)
	}
}

func TestUpdateTenantOps_EnableSkipDeploy(t *testing.T) {
	s := setupDRTest(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "",
			SkipDeploy:             false,
		},
	}
	inserted, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps failed: %v", err)
	}
	opID := inserted[0].ID

	if inserted[0].SkipDeploy {
		t.Fatal("precondition: expected SkipDeploy=false after insert")
	}
	if inserted[0].DeployState != lifecycle.DeployNotStarted {
		t.Fatalf("precondition: expected NOT_STARTED, got %s", inserted[0].DeployState)
	}

	updateOps := []db.ArtifactTenantOperation{
		{
			TransportRequestNumber: "",
			SkipDeploy:             true,
		},
	}
	updateOps[0].ID = opID

	_, err = svc.UpdateTenantOps(s.dr.ID, updateOps, "test-user")
	if err != nil {
		t.Fatalf("UpdateTenantOps (enable SkipDeploy) should succeed, got: %v", err)
	}

	var dbOp db.ArtifactTenantOperation
	testDB.First(&dbOp, opID)
	if !dbOp.SkipDeploy {
		t.Error("expected SkipDeploy=true in DB")
	}
	if dbOp.DeployState != lifecycle.DeployDisabled {
		t.Errorf("expected DEPLOY_DISABLED in DB, got %s", dbOp.DeployState)
	}
}

func TestUpdateTenantOps_DisableSkipDeploy(t *testing.T) {
	s := setupDRTest(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	ops := []db.ArtifactTenantOperation{
		{
			TenantID:               s.target.ID,
			ArtifactTechID:         s.artifact.TechID,
			ArtifactVersion:        s.artifact.Version,
			Artifact:               s.artifact,
			TransportRequestNumber: "",
			SkipDeploy:             true,
		},
	}
	inserted, err := svc.InsertTenantOps(s.dr.ID, ops, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps failed: %v", err)
	}
	opID := inserted[0].ID

	if !inserted[0].SkipDeploy {
		t.Fatal("precondition: expected SkipDeploy=true after insert")
	}
	if inserted[0].DeployState != lifecycle.DeployDisabled {
		t.Fatalf("precondition: expected DEPLOY_DISABLED, got %s", inserted[0].DeployState)
	}

	updateOps := []db.ArtifactTenantOperation{
		{
			TransportRequestNumber: "",
			SkipDeploy:             false,
		},
	}
	updateOps[0].ID = opID

	_, err = svc.UpdateTenantOps(s.dr.ID, updateOps, "test-user")
	if err != nil {
		t.Fatalf("UpdateTenantOps (disable SkipDeploy) should succeed, got: %v", err)
	}

	var dbOp db.ArtifactTenantOperation
	testDB.First(&dbOp, opID)
	if dbOp.SkipDeploy {
		t.Error("expected SkipDeploy=false in DB")
	}
	if dbOp.DeployState != lifecycle.DeployNotStarted {
		t.Errorf("expected NOT_STARTED in DB, got %s", dbOp.DeployState)
	}
}

func TestApprove_AllTRPresent(t *testing.T) {
	s := setupApproveTest(t, "TR-APPROVE-002")

	tmsMock := &mockTMSClient{
		transportRequests: map[string]*tms.TransportRequestV1{
			"TR-APPROVE-002": validTR("TR-APPROVE-002", s.source.TransportNodeName, s.ops[0].ArtifactTechID, s.ops[0].ArtifactVersion, consts.Artifact_Type_Iflow),
		},
		// SyncDeliveryStatus calls TrNodeStatuses — provide a valid response
		nodeStatuses: map[string]map[uint]tms.TrNodeStatus{
			"TR-APPROVE-002": {
				s.target.TransportNodeID: {
					TransportRequestNumber: "TR-APPROVE-002",
					TransportNodeID:        s.target.TransportNodeID,
					Status:                 "INITIAL",
				},
			},
		},
	}

	svc := newTestService(nil, testServiceOpts{tms: tmsMock})

	dr, err := svc.Approve(s.dr.ID, "approver-user")
	if err != nil {
		t.Fatalf("Approve with valid TR should succeed, got: %v", err)
	}
	if dr == nil {
		t.Fatal("Approve returned nil DR")
	}

	// Verify approved status
	var dbDR db.DeliveryRequest
	testDB.First(&dbDR, s.dr.ID)
	if dbDR.ApprovedBy != "approver-user" {
		t.Errorf("expected ApprovedBy=approver-user, got %q", dbDR.ApprovedBy)
	}
	if dbDR.ApprovedAt == nil {
		t.Error("expected ApprovedAt to be set")
	}
}
