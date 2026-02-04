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
