package service

import (
	"testing"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

func TestIsDRTerminal_TerminalStatuses(t *testing.T) {
	svc := newTestService(nil)
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "terminal-test-"+t.Name())

	now := time.Now()

	tests := []struct {
		name     string
		status   lifecycle.AggregateStatus
		terminal bool
	}{
		{"Deployed is terminal", lifecycle.AggDeployed, true},
		{"Canceled is terminal", lifecycle.AggCanceled, true},
		{"ImportFailed is terminal", lifecycle.AggImportFailed, true},
		{"DeployFailed is terminal", lifecycle.AggDeployFailed, true},
		{"WaitingDeploy is terminal", lifecycle.AggWaitingDeploy, true},
		{"AwaitingImport is NOT terminal", lifecycle.AggAwaitingImport, false},
		{"Importing is NOT terminal", lifecycle.AggImporting, false},
		{"Deploying is NOT terminal", lifecycle.AggDeploying, false},
		{"Pending is NOT terminal", lifecycle.AggPending, false},
		{"WaitingApproval is NOT terminal", lifecycle.AggWaitingApprove, false},
		{"InProgress is NOT terminal", lifecycle.AggInProgress, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dr := seedDeliveryRequest(t, tc, db.DeliveryRequest{
				Name:            "DR-" + tt.name,
				SourceTenantID:  tenant.ID,
				AggregateStatus: tt.status,
				ApprovedBy:      "tester",
				ApprovedAt:      &now,
				CreatedBy:       "test",
				UpdatedBy:       "test",
			})

			got := svc.isDRTerminal(dr.ID)
			if got != tt.terminal {
				t.Fatalf("isDRTerminal(%s) = %v, want %v", tt.status, got, tt.terminal)
			}
		})
	}
}

func TestIsDRTerminal_NotFound(t *testing.T) {
	svc := newTestService(nil)
	if !svc.isDRTerminal(999999) {
		t.Fatal("expected terminal=true for non-existent DR")
	}
}

func TestRecoverActiveSyncs_SkipsTerminalAndUnapproved(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "recover-test-"+t.Name())
	now := time.Now()

	// Terminal: should NOT be recovered
	seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "deployed-dr",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggDeployed,
		ApprovedBy:      "tester",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	// Not approved: should NOT be recovered
	seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "unapproved-dr",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggAwaitingImport,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	// Active + approved: SHOULD be recovered
	activeDR := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "active-dr",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggImporting,
		ApprovedBy:      "tester",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	// Awaiting import + approved: SHOULD be recovered (TMS might have regressed state)
	awaitingDR := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "awaiting-dr",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggAwaitingImport,
		ApprovedBy:      "tester",
		ApprovedAt:      &now,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	})

	svc := newTestService(nil)
	svc.SyncTracker = NewSyncTracker()
	svc.RecoverActiveSyncs()

	// Give goroutines a moment to start (they'll fail on SyncDeliveryStatus but that's OK)
	time.Sleep(50 * time.Millisecond)

	// Check that TryStart returns false for recovered DRs (already running)
	if _, started := svc.SyncTracker.TryStart(activeDR.ID); started {
		t.Error("expected activeDR goroutine to be already running")
		svc.SyncTracker.Finish(activeDR.ID)
	}
	if _, started := svc.SyncTracker.TryStart(awaitingDR.ID); started {
		t.Error("expected awaitingDR goroutine to be already running")
		svc.SyncTracker.Finish(awaitingDR.ID)
	}

	svc.SyncTracker.StopAll()
}
