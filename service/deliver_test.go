package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

type deliverFixture struct {
	source db.CpiTenant
	target db.CpiTenant
	other  db.CpiTenant
	dr     db.DeliveryRequest
}

func setupDeliverFixture(t *testing.T) deliverFixture {
	t.Helper()

	tc := newTestCleanup(t)
	suffix := t.Name()

	source := seedTenant(t, tc, "deliver-src-"+suffix)
	source.TmsSourceNodeID = 100
	source.TmsSourceNodeName = "src-node-" + suffix
	if err := testDB.Save(&source).Error; err != nil {
		t.Fatalf("save source tenant: %v", err)
	}

	target := seedTenant(t, tc, "deliver-target-"+suffix)
	target.TmsSourceNodeID = 200
	target.TmsSourceNodeName = "target-node-" + suffix
	if err := testDB.Save(&target).Error; err != nil {
		t.Fatalf("save target tenant: %v", err)
	}

	other := seedTenant(t, tc, "deliver-other-"+suffix)
	other.TmsSourceNodeID = 300
	other.TmsSourceNodeName = "other-node-" + suffix
	if err := testDB.Save(&other).Error; err != nil {
		t.Fatalf("save other tenant: %v", err)
	}

	dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "Deliver Test DR " + suffix,
		SourceTenantID:  source.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	return deliverFixture{
		source: source,
		target: target,
		other:  other,
		dr:     dr,
	}
}

func TestQueryTenantAndQueryOpsWithAcco_ValidateInput(t *testing.T) {
	svc := newTestService(nil)

	if _, err := svc.queryTenant(999999); err == nil || !strings.Contains(err.Error(), "tenant 999999 not found") {
		t.Fatalf("expected not found tenant error, got %v", err)
	}

	if _, err := svc.queryOpsWithAcco(nil); err == nil || !strings.Contains(err.Error(), "no operation ids provided") {
		t.Fatalf("expected no operation ids error, got %v", err)
	}

	fx := setupDeliverFixture(t)
	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.target.ID,
		ArtifactTechID:         "artifact-1",
		ArtifactName:           "Artifact 1",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "1001",
	})

	if _, err := svc.queryOpsWithAcco([]uint{op.ID, op.ID + 9999}); err == nil || !strings.Contains(err.Error(), "artifact operations not found for ids") {
		t.Fatalf("expected missing operation ids error, got %v", err)
	}
}

func TestBatchImportTenantOps_PrecheckAggregatesErrors(t *testing.T) {
	fx := setupDeliverFixture(t)

	opWrongState := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.target.ID,
		ArtifactTechID:         "artifact-wrong-state",
		ArtifactName:           "Wrong State",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "1001",
		ImportState:            lifecycle.ImportNotStarted,
	})
	opWrongNode := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.other.ID,
		ArtifactTechID:         "artifact-wrong-node",
		ArtifactName:           "Wrong Node",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "1002",
		ImportState:            lifecycle.ImportQueued,
	})
	opBadTR := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.target.ID,
		ArtifactTechID:         "artifact-bad-tr",
		ArtifactName:           "Bad TR",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "not-a-number",
		ImportState:            lifecycle.ImportQueued,
	})
	opDowngrade := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.target.ID,
		ArtifactTechID:         "artifact-downgrade",
		ArtifactName:           "Downgrade",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "1004",
		ImportState:            lifecycle.ImportQueued,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClientWithDesignTime{
			iflowVersions: map[string]string{
				"artifact-downgrade": "2.0.0",
			},
		}, nil
	})

	ok, err := svc.BatchImportTenantOps(context.Background(), fx.dr.ID, []uint{opWrongState.ID, opWrongNode.ID, opBadTR.ID, opDowngrade.ID}, fx.target.ID, "tester")
	if ok {
		t.Fatal("expected batch import pre-check to fail")
	}
	if err == nil {
		t.Fatal("expected error when all ops fail pre-check, got nil")
	}
	if !strings.Contains(err.Error(), "all operations failed pre-check") {
		t.Fatalf("expected 'all operations failed' error, got %v", err)
	}

	// verify a warning condition was recorded with skipped operation details
	var conditions []db.Condition
	testDB.Where("delivery_request_id = ? AND state = ?", fx.dr.ID, lifecycle.CondWarn).Find(&conditions)
	if len(conditions) == 0 {
		t.Fatal("expected warning condition for skipped ops")
	}
	for _, opID := range []uint{opWrongState.ID, opWrongNode.ID, opBadTR.ID, opDowngrade.ID} {
		if !strings.Contains(conditions[0].Message, fmt.Sprintf("operation #%d", opID)) {
			t.Fatalf("expected warning condition to include operation #%d", opID)
		}
	}

	var unchanged db.ArtifactTenantOperation
	if err := testDB.First(&unchanged, opDowngrade.ID).Error; err != nil {
		t.Fatalf("reload op: %v", err)
	}
	if unchanged.ImportState != lifecycle.ImportQueued {
		t.Fatalf("expected downgrade op to remain queued, got %s", unchanged.ImportState)
	}
}

func TestBatchImportTenantOps_PartialSkipProceedsWithValidOps(t *testing.T) {
	fx := setupDeliverFixture(t)

	// This op will fail pre-check (version downgrade)
	opDowngrade := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.target.ID,
		ArtifactTechID:         "artifact-downgrade",
		ArtifactName:           "Downgrade",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "2001",
		ImportState:            lifecycle.ImportQueued,
	})
	// This op will pass pre-check (no existing version in target → no downgrade)
	opValid := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.target.ID,
		ArtifactTechID:         "artifact-valid",
		ArtifactName:           "Valid",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "2002",
		ImportState:            lifecycle.ImportQueued,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClientWithDesignTime{
			iflowVersions: map[string]string{
				"artifact-downgrade": "2.0.0", // target has higher version → downgrade error
				// "artifact-valid" not present → mock returns 404 → no downgrade risk
			},
			notFoundAs404: true,
		}, nil
	}, testServiceOpts{
		tms: &mockTMSClient{importTRResult: 999},
	})

	ok, err := svc.BatchImportTenantOps(context.Background(), fx.dr.ID, []uint{opDowngrade.ID, opValid.ID}, fx.target.ID, "tester")

	// Should succeed (import triggered for valid op)
	if !ok {
		t.Fatalf("expected import to proceed with valid ops, got ok=false, err=%v", err)
	}
	// Should return skip error describing the skipped op
	if err == nil {
		t.Fatal("expected skip error for downgrade op, got nil")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("operation #%d", opDowngrade.ID)) {
		t.Fatalf("expected skip error to mention op #%d, got: %s", opDowngrade.ID, err.Error())
	}

	// Verify: valid op moved to InProgress
	waitFor(t, "valid op imported", func() error {
		var validOp db.ArtifactTenantOperation
		if err := testDB.First(&validOp, opValid.ID).Error; err != nil {
			return err
		}
		if validOp.ImportState != lifecycle.ImportInProgress {
			return fmt.Errorf("valid op state = %s, want InProgress", validOp.ImportState)
		}
		return nil
	})

	// Verify: downgrade op remains Queued (untouched)
	var downgradeOp db.ArtifactTenantOperation
	if err := testDB.First(&downgradeOp, opDowngrade.ID).Error; err != nil {
		t.Fatalf("reload downgrade op: %v", err)
	}
	if downgradeOp.ImportState != lifecycle.ImportQueued {
		t.Fatalf("expected downgrade op to remain Queued, got %s", downgradeOp.ImportState)
	}

	// Verify: warning condition recorded
	var conditions []db.Condition
	testDB.Where("delivery_request_id = ? AND state = ?", fx.dr.ID, lifecycle.CondWarn).Find(&conditions)
	if len(conditions) == 0 {
		t.Fatal("expected warning condition for skipped op")
	}
}

func TestBatchImportTenantOps_AsyncFailureRollsBackStateAndRecordsCondition(t *testing.T) {
	fx := setupDeliverFixture(t)

	op := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.target.ID,
		ArtifactTechID:         "artifact-import",
		ArtifactName:           "Import Artifact",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "1234",
		ImportState:            lifecycle.ImportQueued,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClientWithDesignTime{
			iflowVersions: map[string]string{
				"artifact-import": "1.0.0",
			},
		}, nil
	}, testServiceOpts{
		tms: &mockTMSClient{
			importTRErr: errors.New("tms unavailable"),
		},
	})

	ok, err := svc.BatchImportTenantOps(context.Background(), fx.dr.ID, []uint{op.ID}, fx.target.ID, "tester")
	if err != nil {
		t.Fatalf("expected async import to be triggered, got %v", err)
	}
	if !ok {
		t.Fatal("expected batch import to return true before async failure")
	}

	waitFor(t, "import rollback condition", func() error {
		var updated db.ArtifactTenantOperation
		if err := testDB.First(&updated, op.ID).Error; err != nil {
			return err
		}
		if updated.ImportState != lifecycle.ImportFailed {
			return fmt.Errorf("import state = %s, want %s", updated.ImportState, lifecycle.ImportFailed)
		}

		var conditions []db.Condition
		if err := testDB.Where("delivery_request_id = ?", fx.dr.ID).Find(&conditions).Error; err != nil {
			return err
		}
		for _, condition := range conditions {
			if condition.State == lifecycle.CondError && strings.Contains(condition.Message, "batch import failed") {
				return nil
			}
		}
		return errors.New("expected async import failure condition")
	})
}

func TestBatchDeployTenantOps_PrecheckAggregatesErrorsAndSkipsDisabled(t *testing.T) {
	fx := setupDeliverFixture(t)

	opDisabled := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-disabled",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployDisabled,
	})
	opWrongTenant := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.other.ID,
		ArtifactTechID:    "artifact-other",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployQueued,
	})
	opWrongState := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-not-queued",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployNotStarted,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockRuntimeCPI{}, nil
	})

	ok, err := svc.BatchDeployTenantOps(context.Background(), fx.dr.ID, []uint{opDisabled.ID, opWrongTenant.ID, opWrongState.ID}, fx.target.ID, "tester")
	if ok {
		t.Fatal("expected deploy pre-check to fail")
	}
	if err == nil {
		t.Fatal("expected error when all ops fail pre-check, got nil")
	}
	if !strings.Contains(err.Error(), "all operations failed pre-check") {
		t.Fatalf("expected 'all operations failed' error, got %v", err)
	}

	// verify a warning condition was recorded with skipped operation details
	var conditions []db.Condition
	testDB.Where("delivery_request_id = ? AND state = ?", fx.dr.ID, lifecycle.CondWarn).Find(&conditions)
	if len(conditions) == 0 {
		t.Fatal("expected warning condition for skipped ops")
	}
	for _, opID := range []uint{opWrongTenant.ID, opWrongState.ID} {
		if !strings.Contains(conditions[0].Message, fmt.Sprintf("operation #%d", opID)) {
			t.Fatalf("expected warning condition to include operation #%d", opID)
		}
	}

	var unchanged db.ArtifactTenantOperation
	if err := testDB.First(&unchanged, opDisabled.ID).Error; err != nil {
		t.Fatalf("reload disabled op: %v", err)
	}
	if unchanged.DeployState != lifecycle.DeployDisabled {
		t.Fatalf("expected disabled op to remain DEPLOY_DISABLED, got %s", unchanged.DeployState)
	}
}

func TestBatchDeployTenantOps_PartialSkipProceedsWithValidOps(t *testing.T) {
	fx := setupDeliverFixture(t)

	// This op will fail pre-check (wrong tenant)
	opWrongTenant := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.other.ID,
		ArtifactTechID:    "artifact-wrong-tenant",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployQueued,
	})
	// This op will pass pre-check
	opValid := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-deploy-ok",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployQueued,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockRuntimeCPI{}, nil
	})

	ok, err := svc.BatchDeployTenantOps(context.Background(), fx.dr.ID, []uint{opWrongTenant.ID, opValid.ID}, fx.target.ID, "tester")

	// Should succeed (deploy triggered for valid op)
	if !ok {
		t.Fatalf("expected deploy to proceed with valid ops, got ok=false, err=%v", err)
	}
	// Should return skip error describing the skipped op
	if err == nil {
		t.Fatal("expected skip error for wrong-tenant op, got nil")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("operation #%d", opWrongTenant.ID)) {
		t.Fatalf("expected skip error to mention op #%d, got: %s", opWrongTenant.ID, err.Error())
	}

	// Verify: valid op moved to DeployInProgress
	waitFor(t, "valid op deployed", func() error {
		var validOp db.ArtifactTenantOperation
		if err := testDB.First(&validOp, opValid.ID).Error; err != nil {
			return err
		}
		if validOp.DeployState != lifecycle.DeployInProgress {
			return fmt.Errorf("valid op state = %s, want DeployInProgress", validOp.DeployState)
		}
		return nil
	})

	// Verify: wrong-tenant op remains Queued (untouched)
	var wrongOp db.ArtifactTenantOperation
	if err := testDB.First(&wrongOp, opWrongTenant.ID).Error; err != nil {
		t.Fatalf("reload wrong-tenant op: %v", err)
	}
	if wrongOp.DeployState != lifecycle.DeployQueued {
		t.Fatalf("expected wrong-tenant op to remain Queued, got %s", wrongOp.DeployState)
	}

	// Verify: warning condition recorded
	var conditions []db.Condition
	testDB.Where("delivery_request_id = ? AND state = ?", fx.dr.ID, lifecycle.CondWarn).Find(&conditions)
	if len(conditions) == 0 {
		t.Fatal("expected warning condition for skipped op")
	}
}

func TestBatchDeployTenantOps_AsyncPartialFailureRecordsConditions(t *testing.T) {
	fx := setupDeliverFixture(t)

	opOK := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-ok",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployQueued,
	})
	opFail := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-fail",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployQueued,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockRuntimeCPI{
			deployErrs: map[string]error{
				"artifact-fail": errors.New("deploy failed"),
			},
		}, nil
	})

	ok, err := svc.BatchDeployTenantOps(context.Background(), fx.dr.ID, []uint{opOK.ID, opFail.ID}, fx.target.ID, "tester")
	if err != nil {
		t.Fatalf("expected async deploy to be triggered, got %v", err)
	}
	if !ok {
		t.Fatal("expected deploy to return true before async completion")
	}

	waitFor(t, "deploy conditions", func() error {
		var failedOp db.ArtifactTenantOperation
		if err := testDB.First(&failedOp, opFail.ID).Error; err != nil {
			return err
		}
		if failedOp.DeployState != lifecycle.DeployFailed {
			return fmt.Errorf("failed op deploy state = %s, want %s", failedOp.DeployState, lifecycle.DeployFailed)
		}

		var successOp db.ArtifactTenantOperation
		if err := testDB.First(&successOp, opOK.ID).Error; err != nil {
			return err
		}
		if successOp.DeployState != lifecycle.DeployInProgress {
			return fmt.Errorf("success op deploy state = %s, want %s", successOp.DeployState, lifecycle.DeployInProgress)
		}

		var conditions []db.Condition
		if err := testDB.Where("delivery_request_id = ?", fx.dr.ID).Find(&conditions).Error; err != nil {
			return err
		}

		var hasError, hasSuccess bool
		for _, condition := range conditions {
			if condition.State == lifecycle.CondError && strings.Contains(condition.Message, "batch deploy failed") {
				hasError = true
			}
			if condition.State == lifecycle.CondSuccess && strings.Contains(condition.Message, "batch deploy triggered") {
				hasSuccess = true
			}
		}
		if !hasError || !hasSuccess {
			return fmt.Errorf("expected both success and error deploy conditions, got %+v", conditions)
		}
		return nil
	})
}
