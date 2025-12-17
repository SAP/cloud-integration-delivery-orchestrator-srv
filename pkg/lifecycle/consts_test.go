package lifecycle

import "testing"

func TestDeriveAggregateStatus_Importing(t *testing.T) {
	agg := DeriveAggregateStatus(
		AggAwaitingImport,
		[]ImportState{ImportQueued, ImportNotStarted},
		[]DeployState{},
	)
	if agg != AggImporting {
		t.Fatalf("expected IMPORTING, got %s", agg)
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
		[]DeployState{DeployQueued, DeployNotStarted},
	)
	if agg != AggDeploying {
		t.Fatalf("expected DEPLOYING, got %s", agg)
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
