package service

import (
	"context"
	"testing"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cpi"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
)

func TestRecoverActiveSyncs_OnlyRecoversDRsWithActiveOps(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "recover-test-"+t.Name())
	now := time.Now()

	// DR with no InProgress ops: should NOT be recovered
	noOpsDR := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "no-ops-dr",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggAwaitingImport,
		ApprovedBy:      "tester",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})
	// Only Queued ops — not active
	seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: noOpsDR.ID,
		TenantID:          tenant.ID,
		ArtifactTechID:    "queued-artifact",
		ArtifactVersion:   "1.0.0",
		ImportState:       lifecycle.ImportQueued,
		DeployState:       lifecycle.DeployNotStarted,
	})

	// DR with InProgress op: SHOULD be recovered
	activeDR := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "active-dr",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggImporting,
		ApprovedBy:      "tester",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})
	seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: activeDR.ID,
		TenantID:          tenant.ID,
		ArtifactTechID:    "active-artifact",
		ArtifactVersion:   "1.0.0",
		ImportState:       lifecycle.ImportInProgress,
		DeployState:       lifecycle.DeployNotStarted,
	})

	svc := newTestService(nil)
	svc.SyncTracker = NewSyncTracker()
	svc.RecoverActiveSyncs()

	// Give goroutines a moment to start
	time.Sleep(50 * time.Millisecond)

	// activeDR has InProgress ops → goroutine should be running
	if _, started := svc.SyncTracker.TryStart(activeDR.ID); started {
		t.Error("expected activeDR goroutine to be already running")
		svc.SyncTracker.Finish(activeDR.ID)
	}
	// noOpsDR has no InProgress ops → should NOT have been recovered at all
	if _, started := svc.SyncTracker.TryStart(noOpsDR.ID); !started {
		t.Error("expected noOpsDR to not be recovered (no active ops)")
	} else {
		svc.SyncTracker.Finish(noOpsDR.ID)
	}

	svc.SyncTracker.StopAll()
}

func TestRecoverActiveSyncs_RecoversDRWithDeployInProgress(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "deploy-recover-"+t.Name())
	now := time.Now()

	deployDR := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "deploying-dr",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggDeploying,
		ApprovedBy:      "tester",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})
	seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: deployDR.ID,
		TenantID:          tenant.ID,
		ArtifactTechID:    "deploy-artifact",
		ArtifactVersion:   "1.0.0",
		ImportState:       lifecycle.ImportComplete,
		DeployState:       lifecycle.DeployInProgress,
	})

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockRuntimeCPI{runtimeByID: map[string]cpi.RuntimeArtifact{}}, nil
	})
	svc.SyncTracker = NewSyncTracker()
	svc.RecoverActiveSyncs()

	time.Sleep(50 * time.Millisecond)

	// DeployInProgress op → goroutine should be running
	if _, started := svc.SyncTracker.TryStart(deployDR.ID); started {
		t.Error("expected deployDR goroutine to be already running")
		svc.SyncTracker.Finish(deployDR.ID)
	}

	svc.SyncTracker.StopAll()
}
