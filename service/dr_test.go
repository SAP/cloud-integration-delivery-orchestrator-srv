package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
)

// =============================================================================
// Phase 1 Tests: InsertTenantOps, UpdateTenantOps with optional TR
// =============================================================================

// drTestSetup creates tenants, rule, DR for DR operation tests.
type drTestSetup struct {
	tc             *testCleanup
	source         db.CpiTenant
	target         db.CpiTenant
	rule           db.DeliveryRule
	dr             db.DeliveryRequest
	artifactTechID string
	artifactVersion string
	artifactName   string
	artifactType   consts.ArtifactType
	packageID      string
	cpiFactory     IntegrationFactory // pre-configured mock: serves downgrade check
}

func setupDRTest(t *testing.T) drTestSetup {
	t.Helper()
	tc := newTestCleanup(t)

	suffix := t.Name()

	source := seedTenant(t, tc, "dr-src-"+suffix)
	source.TmsSourceNodeID = 100
	source.TmsSourceNodeName = "source-node-" + suffix
	testDB.Save(&source)

	target := seedTenant(t, tc, "dr-tgt-"+suffix)
	target.TmsSourceNodeID = 200
	target.TmsSourceNodeName = "target-node-" + suffix
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

	techID := "iflow-" + suffix
	version := "1.0.5"
	name := "IFlow " + suffix

	// cpiFactory serves PIR lookup (GetPackageArtifactsByType) and downgrade check (GetDesignTimeArtifact).
	// GetPackageArtifactsByType returns the artifact with the real tech ID so resolveTechID succeeds.
	// GetDesignTimeArtifact returns the same version so the downgrade check passes.
	cpiFactory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClientWithDesignTime{
			mockCPIClient: mockCPIClient{
				artifacts: map[string][]cpi.ArtifactCommonItem{
					"pkg1/" + string(consts.Artifact_Type_Iflow): {
						{ID: techID, Name: name, Version: version, PackageID: "pkg1"},
					},
				},
			},
			artifactVersions: map[string]string{techID: version},
		}, nil
	}

	return drTestSetup{
		tc:              tc,
		source:          source,
		target:          target,
		rule:            rule,
		dr:              dr,
		artifactTechID:  techID,
		artifactVersion: version,
		artifactName:    name,
		artifactType:    consts.Artifact_Type_Iflow,
		packageID:       "pkg1",
		cpiFactory:      cpiFactory,
	}
}

// makeOp builds an ArtifactTenantOperation from the test setup's artifact fields.
func (s *drTestSetup) makeOp(tenantID uint, tr string, skipDeploy bool) db.ArtifactTenantOperation {
	return db.ArtifactTenantOperation{
		TenantID:               tenantID,
		ArtifactTechID:         s.artifactTechID,
		ArtifactVersion:        s.artifactVersion,
		ArtifactName:           s.artifactName,
		ArtifactType:           s.artifactType,
		PackageID:              s.packageID,
		TransportRequestNumber: tr,
		SkipDeploy:             skipDeploy,
	}
}

// --- InsertTenantOps Tests ---

func TestInsertTenantOps_EmptyTR(t *testing.T) {
	s := setupDRTest(t)

	svc := newTestService(s.cpiFactory)

	ops := []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "", false)}

	result, err := svc.InsertTenantOps(context.Background(), s.dr.ID, ops, "test-user")
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
			"TR-001": validTR("TR-001", s.source.TmsSourceNodeName, s.artifactTechID, s.artifactVersion, s.artifactType),
		},
	}

	svc := newTestService(s.cpiFactory, testServiceOpts{tms: tmsMock})

	ops := []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "TR-001", false)}

	result, err := svc.InsertTenantOps(context.Background(), s.dr.ID, ops, "test-user")
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
		transportRequests: map[string]*tms.TransportRequestV1{},
	}

	svc := newTestService(s.cpiFactory, testServiceOpts{tms: tmsMock})

	ops := []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "TR-BAD", false)}

	_, err := svc.InsertTenantOps(context.Background(), s.dr.ID, ops, "test-user")
	if err == nil {
		t.Fatal("InsertTenantOps with invalid TR should fail")
	}
}

// --- UpdateTenantOps Tests ---

func TestUpdateTenantOps_EmptyToNonEmpty(t *testing.T) {
	s := setupDRTest(t)

	svc := newTestService(s.cpiFactory)

	inserted, err := svc.InsertTenantOps(context.Background(), s.dr.ID, []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "", false)}, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps failed: %v", err)
	}
	opID := inserted[0].ID

	tmsMock := &mockTMSClient{
		transportRequests: map[string]*tms.TransportRequestV1{
			"TR-002": validTR("TR-002", s.source.TmsSourceNodeName, s.artifactTechID, s.artifactVersion, s.artifactType),
		},
	}
	svc2 := newTestService(s.cpiFactory, testServiceOpts{tms: tmsMock})

	updateOps := []OpUpdateItem{{TransportRequestNumber: "TR-002"}}
	updateOps[0].ID = opID

	result, err := svc2.UpdateTenantOps(s.dr.ID, updateOps, "test-user")
	if err != nil {
		t.Fatalf("UpdateTenantOps (empty → non-empty) should succeed, got: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	var dbOp db.ArtifactTenantOperation
	testDB.First(&dbOp, opID)
	if dbOp.TransportRequestNumber != "TR-002" {
		t.Errorf("expected TR-002 in DB, got %q", dbOp.TransportRequestNumber)
	}
}

func TestUpdateTenantOps_EmptyToEmpty(t *testing.T) {
	s := setupDRTest(t)

	svc := newTestService(s.cpiFactory)

	inserted, err := svc.InsertTenantOps(context.Background(), s.dr.ID, []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "", false)}, "test-user")
	if err != nil {
		t.Fatalf("InsertTenantOps failed: %v", err)
	}
	opID := inserted[0].ID

	svc2 := newTestService(s.cpiFactory)
	updateOps := []OpUpdateItem{{TransportRequestNumber: ""}}
	updateOps[0].ID = opID

	_, err = svc2.UpdateTenantOps(s.dr.ID, updateOps, "test-user")
	if err != nil {
		t.Fatalf("UpdateTenantOps (empty → empty) should succeed without TMS call, got: %v", err)
	}
}

// =============================================================================
// Phase 1 Tests: Approve & RequestApproval TR validation
// =============================================================================

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
	source.TmsSourceNodeID = 300
	source.TmsSourceNodeName = "appr-src-node-" + suffix
	testDB.Save(&source)

	target := seedTenant(t, tc, "appr-tgt-"+suffix)
	target.TmsSourceNodeID = 400
	target.TmsSourceNodeName = "appr-tgt-node-" + suffix
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

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      dr.ID,
		TenantID:               target.ID,
		ArtifactTechID:         "appr-iflow-" + suffix,
		ArtifactVersion:        "2.0.3",
		ArtifactName:           "Approve IFlow " + suffix,
		ArtifactType:           consts.Artifact_Type_Iflow,
		PackageID:              "pkg-appr-" + suffix,
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
	s := setupApproveTest(t, "")

	tmsMock := &mockTMSClient{}
	svc := newTestService(nil, testServiceOpts{tms: tmsMock})

	err := svc.RequestApproval(s.dr.ID, "creator-user", []string{"approver-1"}, "please approve")
	if err == nil {
		t.Fatal("RequestApproval with missing TR should fail")
	}

	var dr db.DeliveryRequest
	testDB.First(&dr, s.dr.ID)
	if dr.AggregateStatus != lifecycle.AggWaitingApprove {
		t.Logf("DR status after failed RequestApproval: %s (unchanged from setup)", dr.AggregateStatus)
	}
}

func TestRequestApproval_WithValidTR(t *testing.T) {
	s := setupApproveTest(t, "TR-APPROVE-001")

	tmsMock := &mockTMSClient{
		transportRequests: map[string]*tms.TransportRequestV1{
			"TR-APPROVE-001": validTR("TR-APPROVE-001", s.source.TmsSourceNodeName, s.ops[0].ArtifactTechID, s.ops[0].ArtifactVersion, consts.Artifact_Type_Iflow),
		},
	}

	svc := newTestService(nil, testServiceOpts{tms: tmsMock})

	err := svc.RequestApproval(s.dr.ID, "creator-user", []string{"approver-1"}, "please approve")
	if err != nil {
		t.Fatalf("RequestApproval with valid TR should succeed, got: %v", err)
	}

	var dr db.DeliveryRequest
	testDB.First(&dr, s.dr.ID)
	if dr.AggregateStatus != lifecycle.AggWaitingApprove {
		t.Errorf("expected WAITING_APPROVAL, got %s", dr.AggregateStatus)
	}
}

func TestApprove_MissingTR(t *testing.T) {
	s := setupApproveTest(t, "")

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

	svc := newTestService(s.cpiFactory)

	ops := []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "", true)}

	result, err := svc.InsertTenantOps(context.Background(), s.dr.ID, ops, "test-user")
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

	svc := newTestService(s.cpiFactory)

	ops := []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "", false)}

	result, err := svc.InsertTenantOps(context.Background(), s.dr.ID, ops, "test-user")
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

	svc := newTestService(s.cpiFactory)

	inserted, err := svc.InsertTenantOps(context.Background(), s.dr.ID, []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "", false)}, "test-user")
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

	updateOps := []OpUpdateItem{{TransportRequestNumber: "", SkipDeploy: true}}
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

	svc := newTestService(s.cpiFactory)

	inserted, err := svc.InsertTenantOps(context.Background(), s.dr.ID, []db.ArtifactTenantOperation{s.makeOp(s.target.ID, "", true)}, "test-user")
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

	updateOps := []OpUpdateItem{{TransportRequestNumber: "", SkipDeploy: false}}
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
			"TR-APPROVE-002": validTR("TR-APPROVE-002", s.source.TmsSourceNodeName, s.ops[0].ArtifactTechID, s.ops[0].ArtifactVersion, consts.Artifact_Type_Iflow),
		},
		nodeStatuses: map[string]map[uint]tms.TrNodeStatus{
			"TR-APPROVE-002": {
				s.target.TmsSourceNodeID: {
					TransportRequestNumber: "TR-APPROVE-002",
					TransportNodeID:        s.target.TmsSourceNodeID,
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

	var dbDR db.DeliveryRequest
	testDB.First(&dbDR, s.dr.ID)
	if dbDR.ApprovedBy != "approver-user" {
		t.Errorf("expected ApprovedBy=approver-user, got %q", dbDR.ApprovedBy)
	}
	if dbDR.ApprovedAt == nil {
		t.Error("expected ApprovedAt to be set")
	}
}

func TestApprove_ConditionWrittenWhenNotifierFails(t *testing.T) {
	s := setupApproveTest(t, "TR-APPROVE-003")

	tmsMock := &mockTMSClient{
		transportRequests: map[string]*tms.TransportRequestV1{
			"TR-APPROVE-003": validTR("TR-APPROVE-003", s.source.TmsSourceNodeName, s.ops[0].ArtifactTechID, s.ops[0].ArtifactVersion, consts.Artifact_Type_Iflow),
		},
		nodeStatuses: map[string]map[uint]tms.TrNodeStatus{
			"TR-APPROVE-003": {
				s.target.TmsSourceNodeID: {
					TransportRequestNumber: "TR-APPROVE-003",
					TransportNodeID:        s.target.TmsSourceNodeID,
					Status:                 "INITIAL",
				},
			},
		},
	}

	// Use a notifier that always fails — simulates destination not found
	failNotifier := &failingNotifier{err: fmt.Errorf("mail service destination 'SMTP_MAIL' not found")}
	svc := newTestService(nil, testServiceOpts{tms: tmsMock, notifier: failNotifier})

	dr, err := svc.Approve(s.dr.ID, "approver-user")
	if err != nil {
		t.Fatalf("Approve should succeed even when notifier fails, got: %v", err)
	}
	if dr == nil {
		t.Fatal("Approve returned nil DR")
	}

	// Verify DB update completed
	var dbDR db.DeliveryRequest
	testDB.First(&dbDR, s.dr.ID)
	if dbDR.ApprovedBy != "approver-user" {
		t.Errorf("expected ApprovedBy=approver-user, got %q", dbDR.ApprovedBy)
	}
	if dbDR.AggregateStatus != lifecycle.AggAwaitingImport {
		t.Errorf("expected AggregateStatus=%s, got %s", lifecycle.AggAwaitingImport, dbDR.AggregateStatus)
	}

	// Verify the "approved by" condition was written (before notification goroutine)
	var conditions []db.Condition
	testDB.Where("delivery_request_id = ?", s.dr.ID).Find(&conditions)

	found := false
	for _, c := range conditions {
		if c.State == lifecycle.CondSuccess && strings.Contains(c.Message, "approved by") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'approved by' condition to be written even when notifier fails")
	}
}
