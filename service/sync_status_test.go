package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
)

type syncFixture struct {
	source db.CpiTenant
	target db.CpiTenant
	extra  db.CpiTenant
	rule   db.DeliveryRule
	dr     db.DeliveryRequest
}

func setupSyncFixture(t *testing.T) syncFixture {
	t.Helper()

	tc := newTestCleanup(t)
	suffix := t.Name()

	source := seedTenant(t, tc, "sync-src-"+suffix)
	source.TmsSourceNodeID = 100
	source.TmsSourceNodeName = "sync-src-node-" + suffix
	if err := testDB.Save(&source).Error; err != nil {
		t.Fatalf("save source tenant: %v", err)
	}

	target := seedTenant(t, tc, "sync-target-"+suffix)
	target.TmsSourceNodeID = 200
	target.TmsSourceNodeName = "sync-target-node-" + suffix
	if err := testDB.Save(&target).Error; err != nil {
		t.Fatalf("save target tenant: %v", err)
	}

	extra := seedTenant(t, tc, "sync-extra-"+suffix)
	extra.TmsSourceNodeID = 300
	extra.TmsSourceNodeName = "sync-extra-node-" + suffix
	if err := testDB.Save(&extra).Error; err != nil {
		t.Fatalf("save extra tenant: %v", err)
	}

	rule := seedRule(t, tc, "sync-rule-"+suffix, source, []db.CpiTenant{source, target, extra}, true)
	rule.TargetNodes = []db.TransportNode{
		{ID: target.TmsSourceNodeID, Name: target.TmsSourceNodeName},
	}
	if err := testDB.Save(&rule).Error; err != nil {
		t.Fatalf("save delivery rule: %v", err)
	}

	now := time.Now()
	dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "Sync Test DR " + suffix,
		SourceTenantID:  source.ID,
		DeliveryRuleID:  rule.ID,
		AggregateStatus: lifecycle.AggPending,
		ApprovedBy:      "approver",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	return syncFixture{
		source: source,
		target: target,
		extra:  extra,
		rule:   rule,
		dr:     dr,
	}
}

func TestDetermineOverallStatus_IgnoresSourceTenantOps(t *testing.T) {
	fx := setupSyncFixture(t)
	svc := newTestService(nil)

	seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.source.ID,
		ArtifactTechID:    "artifact-source",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		ImportState:       lifecycle.ImportFailed,
		DeployState:       lifecycle.DeployNotStarted,
	})
	seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-target",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		ImportState:       lifecycle.ImportComplete,
		DeployState:       lifecycle.DeployQueued,
	})

	if err := svc.DetermineOverallStatus(fx.dr.ID); err != nil {
		t.Fatalf("DetermineOverallStatus failed: %v", err)
	}

	var updated db.DeliveryRequest
	if err := testDB.First(&updated, fx.dr.ID).Error; err != nil {
		t.Fatalf("reload delivery request: %v", err)
	}
	if updated.AggregateStatus != lifecycle.AggWaitingDeploy {
		t.Fatalf("aggregate status = %s, want %s", updated.AggregateStatus, lifecycle.AggWaitingDeploy)
	}
}

func TestSyncDeliveryStatus_GuardsConcurrentUnapprovedAndCanceledRequests(t *testing.T) {
	svc := newTestService(nil)
	svc.drSyncLocks.Store(uint(77), struct{}{})
	t.Cleanup(func() {
		svc.drSyncLocks.Delete(uint(77))
	})

	if err := svc.SyncDeliveryStatus(77, "tester"); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("expected concurrent sync guard error, got %v", err)
	}

	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "sync-guard-tenant-"+t.Name())
	notApproved := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "not-approved-" + t.Name(),
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	if err := svc.SyncDeliveryStatus(notApproved.ID, "tester"); err == nil || !strings.Contains(err.Error(), "has not been approved yet") {
		t.Fatalf("expected approval guard error, got %v", err)
	}

	now := time.Now()
	canceled := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "canceled-" + t.Name(),
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggCanceled,
		ApprovedBy:      "approver",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	if err := svc.SyncDeliveryStatus(canceled.ID, "tester"); err == nil || !strings.Contains(err.Error(), "already canceled") {
		t.Fatalf("expected canceled guard error, got %v", err)
	}
}

func TestSyncDeployState_UpdatesRuntimeStatesAndCreatesConditions(t *testing.T) {
	fx := setupSyncFixture(t)

	opSuccess := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-success",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployInProgress,
	})
	opMismatch := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-mismatch",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployInProgress,
	})
	opError := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-error",
		ArtifactVersion:   "1.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployInProgress,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockRuntimeCPI{
			runtimeByID: map[string]cpi.RuntimeArtifact{
				"artifact-success": {
					ID:         "artifact-success",
					Version:    "1.0.0",
					Status:     consts.Artifact_Rt_Started,
					DeployedBy: "tester@example.com",
					DeployedOn: "2026-04-27T15:00:00Z",
				},
				"artifact-mismatch": {
					ID:      "artifact-mismatch",
					Version: "2.0.0",
					Status:  consts.Artifact_Rt_Started,
				},
			},
			runtimeErrs: map[string]error{
				"artifact-error": errors.New("runtime artifact missing"),
			},
		}, nil
	})

	conditions := svc.syncDeployState(fx.dr.ID, "tester")
	if len(conditions) != 3 {
		t.Fatalf("expected 3 conditions, got %d", len(conditions))
	}

	var updatedSuccess db.ArtifactTenantOperation
	if err := testDB.First(&updatedSuccess, opSuccess.ID).Error; err != nil {
		t.Fatalf("reload success op: %v", err)
	}
	if updatedSuccess.DeployState != lifecycle.DeployComplete {
		t.Fatalf("success op deploy state = %s, want %s", updatedSuccess.DeployState, lifecycle.DeployComplete)
	}

	var updatedMismatch db.ArtifactTenantOperation
	if err := testDB.First(&updatedMismatch, opMismatch.ID).Error; err != nil {
		t.Fatalf("reload mismatch op: %v", err)
	}
	if updatedMismatch.DeployState != lifecycle.DeployFailed {
		t.Fatalf("mismatch op deploy state = %s, want %s", updatedMismatch.DeployState, lifecycle.DeployFailed)
	}

	var updatedError db.ArtifactTenantOperation
	if err := testDB.First(&updatedError, opError.ID).Error; err != nil {
		t.Fatalf("reload error op: %v", err)
	}
	if updatedError.DeployState != lifecycle.DeployInProgress {
		t.Fatalf("error op deploy state = %s, want %s", updatedError.DeployState, lifecycle.DeployInProgress)
	}

	var hasSuccess, hasMismatchWarn, hasRuntimeWarn bool
	for _, condition := range conditions {
		if condition.State == lifecycle.CondSuccess && strings.Contains(condition.Message, "deployed in") {
			hasSuccess = true
		}
		if condition.State == lifecycle.CondWarn && strings.Contains(condition.Message, "is higher than expected version") {
			hasMismatchWarn = true
		}
		if condition.State == lifecycle.CondWarn && strings.Contains(condition.Message, "may not deployed yet") {
			hasRuntimeWarn = true
		}
	}
	if !hasSuccess || !hasMismatchWarn || !hasRuntimeWarn {
		t.Fatalf("unexpected deploy conditions: %+v", conditions)
	}

	// Second sync: mismatch op is now DeployFailed, should NOT produce duplicate condition
	conditions2 := svc.syncDeployState(fx.dr.ID, "tester")
	for _, c := range conditions2 {
		if strings.Contains(c.Message, "is higher than expected version") {
			t.Fatal("version mismatch condition should not repeat on second sync")
		}
	}
}

func TestSyncDeployState_PendingDeploy_SkipsWhenRuntimeVersionLower(t *testing.T) {
	fx := setupSyncFixture(t)

	// Op expects version 2.0.0 but runtime still shows 1.0.0 (deploy hasn't taken effect)
	opPending := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: fx.dr.ID,
		TenantID:          fx.target.ID,
		ArtifactTechID:    "artifact-pending",
		ArtifactVersion:   "2.0.0",
		ArtifactType:      consts.Artifact_Type_Iflow,
		DeployState:       lifecycle.DeployInProgress,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockRuntimeCPI{
			runtimeByID: map[string]cpi.RuntimeArtifact{
				"artifact-pending": {
					ID:      "artifact-pending",
					Version: "1.0.0", // lower than expected 2.0.0
					Status:  consts.Artifact_Rt_Started,
				},
			},
		}, nil
	})

	conditions := svc.syncDeployState(fx.dr.ID, "tester")

	// Should produce no conditions (silently skipped)
	for _, c := range conditions {
		if c.ArtifactTenantOperationID == opPending.ID {
			t.Fatalf("expected no condition for pending deploy, got: %s", c.Message)
		}
	}

	// Op should remain DeployInProgress (not changed to DeployFailed)
	var updated db.ArtifactTenantOperation
	if err := testDB.First(&updated, opPending.ID).Error; err != nil {
		t.Fatalf("reload pending op: %v", err)
	}
	if updated.DeployState != lifecycle.DeployInProgress {
		t.Fatalf("pending op deploy state = %s, want %s", updated.DeployState, lifecycle.DeployInProgress)
	}
}

func TestSyncImportState_CreatesTargetOpsAndWarningConditions(t *testing.T) {
	fx := setupSyncFixture(t)

	sourceOp := seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      fx.dr.ID,
		TenantID:               fx.source.ID,
		ArtifactTechID:         "artifact-import",
		ArtifactName:           "Artifact Import",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		PackageID:              "pkg-import",
		PackageName:            "Package Import",
		PackageVersion:         "1.0.0",
		TransportRequestNumber: "TR-9000",
		ImportState:            lifecycle.ImportQueued,
		DeployState:            lifecycle.DeployNotStarted,
	})

	now := time.Now()
	svc := newTestService(nil, testServiceOpts{
		tms: &mockStatusTMS{
			mockTMSClient: mockTMSClient{
				nodeStatuses: map[string]map[uint]tms.TrNodeStatus{
					"TR-9000": {
						fx.target.TmsSourceNodeID: {
							TransportRequestNumber: "TR-9000",
							TransportNodeID:        fx.target.TmsSourceNodeID,
							TransportNodeName:      fx.target.TmsSourceNodeName,
							Status:                 "WARNING",
							UpdatedAt:              now,
						},
						fx.extra.TmsSourceNodeID: {
							TransportRequestNumber: "TR-9000",
							TransportNodeID:        fx.extra.TmsSourceNodeID,
							TransportNodeName:      fx.extra.TmsSourceNodeName,
							Status:                 "SUCCEEDED",
							UpdatedAt:              now,
						},
					},
				},
			},
			warnLogs: map[string][]string{
				tmsLogKey("TR-9000", fx.target.TmsSourceNodeID): []string{"warning line 1", "warning line 2"},
			},
		},
	})

	conditions := svc.syncImportState(fx.dr.ID, "tester")
	if len(conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conditions))
	}

	var targetOp db.ArtifactTenantOperation
	if err := testDB.Where("delivery_request_id = ? AND tenant_id = ? AND transport_request_number = ?", fx.dr.ID, fx.target.ID, sourceOp.TransportRequestNumber).
		First(&targetOp).Error; err != nil {
		t.Fatalf("expected created target operation: %v", err)
	}
	if targetOp.ImportState != lifecycle.ImportComplete {
		t.Fatalf("target op import state = %s, want %s", targetOp.ImportState, lifecycle.ImportComplete)
	}
	if targetOp.DeployState != lifecycle.DeployQueued {
		t.Fatalf("target op deploy state = %s, want %s", targetOp.DeployState, lifecycle.DeployQueued)
	}
	if targetOp.CreatedBy != "tester" || targetOp.UpdatedBy != "tester" {
		t.Fatalf("expected created/updated by tester, got createdBy=%q updatedBy=%q", targetOp.CreatedBy, targetOp.UpdatedBy)
	}

	var extraCount int64
	if err := testDB.Model(&db.ArtifactTenantOperation{}).
		Where("delivery_request_id = ? AND tenant_id = ? AND transport_request_number = ?", fx.dr.ID, fx.extra.ID, sourceOp.TransportRequestNumber).
		Count(&extraCount).Error; err != nil {
		t.Fatalf("count extra ops: %v", err)
	}
	if extraCount != 0 {
		t.Fatalf("expected no operation for skipped node outside rule target list, got %d", extraCount)
	}

	var hasSuccess, hasWarn bool
	for _, condition := range conditions {
		if condition.State == lifecycle.CondSuccess && strings.Contains(condition.Message, "successfully imported") {
			hasSuccess = true
		}
		if condition.State == lifecycle.CondWarn && strings.Contains(condition.Message, "warning line 1") {
			hasWarn = true
		}
	}
	if !hasSuccess || !hasWarn {
		t.Fatalf("unexpected import conditions: %+v", conditions)
	}
}

func TestExtractJiraIssueKey(t *testing.T) {
	svc := newTestService(nil)

	if got := svc.extractJiraIssueKey("https://jira.tools.sap/browse/MACOMMT-32980"); got != "MACOMMT-32980" {
		t.Fatalf("issue key = %q, want MACOMMT-32980", got)
	}
	if got := svc.extractJiraIssueKey("https://jira.tools.sap/projects/MACOMMT"); got != "" {
		t.Fatalf("expected empty issue key for non-browse URL, got %q", got)
	}
}
