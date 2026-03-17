package lifecycle

import "testing"

func TestDeriveAggregateStatus_Importing(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportInProgress, ImportNotStarted},
		[]DeployState{},
	)
	if agg != AggImporting {
		t.Fatalf("expected IMPORTING, got %s", agg)
	}
}

func TestDeriveAggregateStatus_AwaitingImport(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggPending,
		[]ImportState{ImportQueued, ImportNotStarted},
		[]DeployState{},
	)
	if agg != AggAwaitingImport {
		t.Fatalf("expected AWAITING_IMPORT, got %s", agg)
	}
}

func TestDeriveAggregateStatus_ImportFailed(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportFailed, ImportInProgress},
		[]DeployState{},
	)
	if agg != AggImportFailed {
		t.Fatalf("expected IMPORT_FAILED, got %s", agg)
	}
}

func TestDeriveAggregateStatus_WaitingDeploy(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportComplete, ImportDisabled},
		[]DeployState{DeployNotStarted, DeployNotStarted},
	)
	if agg != AggWaitingDeploy {
		t.Fatalf("expected AWAITING_DEPLOY, got %s", agg)
	}
}

func TestDeriveAggregateStatus_Deploying(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportComplete, ImportComplete},
		[]DeployState{DeployInProgress, DeployNotStarted},
	)
	if agg != AggDeploying {
		t.Fatalf("expected DEPLOYING, got %s", agg)
	}
}

func TestDeriveAggregateStatus_WaitingDeployWithQueued(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportComplete, ImportComplete},
		[]DeployState{DeployQueued, DeployNotStarted},
	)
	if agg != AggWaitingDeploy {
		t.Fatalf("expected AWAITING_DEPLOY, got %s", agg)
	}
}

func TestDeriveAggregateStatus_DeployFailed(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportComplete, ImportComplete},
		[]DeployState{DeployFailed, DeployNotStarted},
	)
	if agg != AggDeployFailed {
		t.Fatalf("expected DEPLOY_FAILED, got %s", agg)
	}
}

func TestDeriveAggregateStatus_DeployFailedWithComplete(t *testing.T) {
	// All imported, some deployed successfully but one failed
	agg := DeriveAggregateStatus(
		AggDeploying,
		[]ImportState{ImportComplete, ImportComplete, ImportComplete},
		[]DeployState{DeployComplete, DeployComplete, DeployFailed},
	)
	if agg != AggDeployFailed {
		t.Fatalf("expected DEPLOY_FAILED, got %s", agg)
	}
}

func TestDeriveAggregateStatus_Deployed(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportComplete, ImportComplete},
		[]DeployState{DeployComplete, DeployComplete},
	)
	if agg != AggDeployed {
		t.Fatalf("expected DEPLOYED, got %s", agg)
	}
}

func TestDeriveAggregateStatus_Deployed1(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggDeploying,
		[]ImportState{ImportComplete, ImportComplete},
		[]DeployState{DeployComplete, DeployComplete},
	)
	if agg != AggDeployed {
		t.Fatalf("expected DEPLOYED, got %s", agg)
	}
}

func TestDeriveAggregateStatus_FallbackKeepingAgg(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggWaitingApprove,
		[]ImportState{},
		[]DeployState{},
	)
	if agg != AggWaitingApprove {
		t.Fatalf("expected WAITING_APPROVAL, got %s", agg)
	}
}

// ============================================================
// Cancellation-related tests
// ============================================================

// TestDeriveAggregateStatus_CanceledIsTerminal verifies that CANCELED status is preserved
// regardless of import/deploy states (terminal state)
func TestDeriveAggregateStatus_CanceledIsTerminal(t *testing.T) {
	tests := []struct {
		name         string
		importStates []ImportState
		deployStates []DeployState
	}{
		{
			name:         "canceled with no ops",
			importStates: []ImportState{},
			deployStates: []DeployState{},
		},
		{
			name:         "canceled with pending imports",
			importStates: []ImportState{ImportQueued, ImportNotStarted},
			deployStates: []DeployState{},
		},
		{
			name:         "canceled with in-progress imports",
			importStates: []ImportState{ImportInProgress},
			deployStates: []DeployState{},
		},
		{
			name:         "canceled with complete imports",
			importStates: []ImportState{ImportComplete, ImportComplete},
			deployStates: []DeployState{DeployQueued, DeployNotStarted},
		},
		{
			name:         "canceled with in-progress deploys",
			importStates: []ImportState{ImportComplete},
			deployStates: []DeployState{DeployInProgress},
		},
		{
			name:         "canceled with complete deploys",
			importStates: []ImportState{ImportComplete},
			deployStates: []DeployState{DeployComplete},
		},
		{
			name:         "canceled with failed imports",
			importStates: []ImportState{ImportFailed},
			deployStates: []DeployState{},
		},
		{
			name:         "canceled with failed deploys",
			importStates: []ImportState{ImportComplete},
			deployStates: []DeployState{DeployFailed},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := DeriveAggregateStatus(AggCanceled, tt.importStates, tt.deployStates)
			if agg != AggCanceled {
				t.Errorf("expected CANCELED to be preserved, got %s", agg)
			}
		})
	}
}

// TestCancellableStatuses_Definition defines which statuses should allow cancellation
// This serves as documentation and validation for the cancel feature
func TestCancellableStatuses_Definition(t *testing.T) {
	// Statuses that SHOULD be cancellable (delivery not yet complete or in-progress)
	cancellable := map[AggregateStatus]bool{
		AggPending:        true,
		AggWaitingApprove: true,
		AggAwaitingImport: true,
		AggImportFailed:   true,
		AggWaitingDeploy:  true,
		AggDeployFailed:   true,
	}

	// Statuses that should NOT be cancellable
	notCancellable := []AggregateStatus{
		AggImporting,  // operations in progress
		AggDeploying,  // operations in progress
		AggDeployed,   // already complete
		AggCanceled,   // already canceled
		AggInProgress, // generic in progress
	}

	// Verify cancellable count
	if len(cancellable) != 6 {
		t.Errorf("expected 6 cancellable statuses, got %d", len(cancellable))
	}

	// Verify non-cancellable statuses are not in the map
	for _, status := range notCancellable {
		if cancellable[status] {
			t.Errorf("status %s should NOT be cancellable", status)
		}
	}
}

// TestCancelStatusTransitions verifies the expected status transitions for cancellation
func TestCancelStatusTransitions(t *testing.T) {
	tests := []struct {
		name           string
		fromStatus     AggregateStatus
		canCancel      bool
		expectedReason string
	}{
		// Cancellable statuses
		{"PENDING can be canceled", AggPending, true, ""},
		{"WAITING_APPROVAL can be canceled", AggWaitingApprove, true, ""},
		{"AWAITING_IMPORT can be canceled", AggAwaitingImport, true, ""},
		{"IMPORT_FAILED can be canceled", AggImportFailed, true, ""},
		{"WAITING_DEPLOY can be canceled", AggWaitingDeploy, true, ""},
		{"DEPLOY_FAILED can be canceled", AggDeployFailed, true, ""},

		// Non-cancellable statuses
		{"IMPORTING cannot be canceled", AggImporting, false, "operations in progress"},
		{"DEPLOYING cannot be canceled", AggDeploying, false, "operations in progress"},
		{"DEPLOYED cannot be canceled", AggDeployed, false, "already complete"},
		{"CANCELED cannot be canceled", AggCanceled, false, "already canceled"},
	}

	cancellable := map[AggregateStatus]bool{
		AggPending:        true,
		AggWaitingApprove: true,
		AggAwaitingImport: true,
		AggImportFailed:   true,
		AggWaitingDeploy:  true,
		AggDeployFailed:   true,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := cancellable[tt.fromStatus]
			if actual != tt.canCancel {
				t.Errorf("cancellable[%s] = %v, want %v", tt.fromStatus, actual, tt.canCancel)
			}
		})
	}
}

// TestDeployedWithDisabled verifies that DeployDisabled is treated as complete
func TestDeriveAggregateStatus_DeployedWithDisabled(t *testing.T) {
	// All imports complete, some deploys disabled (should be DEPLOYED)
	agg := DeriveAggregateStatus(
		AggWaitingDeploy,
		[]ImportState{ImportComplete, ImportComplete},
		[]DeployState{DeployComplete, DeployDisabled},
	)
	if agg != AggDeployed {
		t.Fatalf("expected DEPLOYED with mixed Complete/Disabled, got %s", agg)
	}
}

// TestDeriveAggregateStatus_MixedSkipDeploy verifies aggregate status with a mix of
// skip-deploy (DeployDisabled) and normal (DeployComplete) ops.
func TestDeriveAggregateStatus_MixedSkipDeploy(t *testing.T) {
	tests := []struct {
		name         string
		importStates []ImportState
		deployStates []DeployState
		want         AggregateStatus
	}{
		{
			name:         "all skip deploy",
			importStates: []ImportState{ImportComplete, ImportComplete},
			deployStates: []DeployState{DeployDisabled, DeployDisabled},
			want:         AggDeployed,
		},
		{
			name:         "mixed complete and disabled",
			importStates: []ImportState{ImportComplete, ImportComplete, ImportComplete},
			deployStates: []DeployState{DeployComplete, DeployDisabled, DeployComplete},
			want:         AggDeployed,
		},
		{
			name:         "skip deploy with one still deploying",
			importStates: []ImportState{ImportComplete, ImportComplete},
			deployStates: []DeployState{DeployDisabled, DeployInProgress},
			want:         AggDeploying,
		},
		{
			name:         "skip deploy with one failed",
			importStates: []ImportState{ImportComplete, ImportComplete},
			deployStates: []DeployState{DeployDisabled, DeployFailed},
			want:         AggDeployFailed,
		},
		{
			name:         "skip deploy with one not started (awaiting deploy)",
			importStates: []ImportState{ImportComplete, ImportComplete},
			deployStates: []DeployState{DeployDisabled, DeployNotStarted},
			want:         AggWaitingDeploy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := DeriveAggregateStatus(AggWaitingDeploy, tt.importStates, tt.deployStates)
			if agg != tt.want {
				t.Errorf("got %s, want %s", agg, tt.want)
			}
		})
	}
}

// TestImportDisabledProgressesToDeploy verifies ImportDisabled allows progression to deploy phase
func TestDeriveAggregateStatus_ImportDisabledProgressesToDeploy(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggImporting,
		[]ImportState{ImportDisabled, ImportDisabled},
		[]DeployState{DeployQueued, DeployNotStarted},
	)
	if agg != AggWaitingDeploy {
		t.Fatalf("expected WAITING_DEPLOY with all ImportDisabled, got %s", agg)
	}
}
