package service

import (
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
)

func TestCaptureDrSnapshot_OpsKeyEquality(t *testing.T) {
	svc := newTestService(nil)

	// Create a DR with two ops
	dr := db.DeliveryRequest{
		AggregateStatus: lifecycle.AggImporting,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	}
	if err := svc.DB.Create(&dr).Error; err != nil {
		t.Fatal(err)
	}
	op1 := db.ArtifactTenantOperation{
		DeliveryRequestID: dr.ID,
		ImportState:       lifecycle.ImportNotStarted,
		DeployState:       lifecycle.DeployNotStarted,
	}
	op2 := db.ArtifactTenantOperation{
		DeliveryRequestID: dr.ID,
		ImportState:       lifecycle.ImportComplete,
		DeployState:       lifecycle.DeployNotStarted,
	}
	svc.DB.Create(&op1)
	svc.DB.Create(&op2)

	// Capture twice with no changes → OpsKey should be identical
	snap1 := svc.captureDrSnapshot(dr.ID)
	snap2 := svc.captureDrSnapshot(dr.ID)

	if !snap1.Exists || !snap2.Exists {
		t.Fatal("snapshot should exist")
	}
	if snap1.OpsKey != snap2.OpsKey {
		t.Errorf("OpsKey should be equal when nothing changed\n  got1: %s\n  got2: %s", snap1.OpsKey, snap2.OpsKey)
	}
	if snap1.Status != snap2.Status {
		t.Errorf("Status should be equal: %s vs %s", snap1.Status, snap2.Status)
	}

	// Mutate op1's import state → OpsKey should differ
	svc.DB.Model(&op1).Update("import_state", lifecycle.ImportComplete)
	snap3 := svc.captureDrSnapshot(dr.ID)

	if snap3.OpsKey == snap1.OpsKey {
		t.Errorf("OpsKey should differ after import state change\n  before: %s\n  after:  %s", snap1.OpsKey, snap3.OpsKey)
	}

	// Mutate op2's deploy state → OpsKey should differ again
	svc.DB.Model(&op2).Update("deploy_state", lifecycle.DeployComplete)
	snap4 := svc.captureDrSnapshot(dr.ID)

	if snap4.OpsKey == snap3.OpsKey {
		t.Errorf("OpsKey should differ after deploy state change\n  before: %s\n  after:  %s", snap3.OpsKey, snap4.OpsKey)
	}

	// Cleanup
	svc.DB.Where("delivery_request_id = ?", dr.ID).Delete(&db.ArtifactTenantOperation{})
	svc.DB.Delete(&dr)
}

func TestCaptureDrSnapshot_StatusChange(t *testing.T) {
	svc := newTestService(nil)

	dr := db.DeliveryRequest{
		AggregateStatus: lifecycle.AggImporting,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	}
	svc.DB.Create(&dr)

	snap1 := svc.captureDrSnapshot(dr.ID)

	// Change aggregate status
	svc.DB.Model(&dr).Update("aggregate_status", lifecycle.AggDeploying)
	snap2 := svc.captureDrSnapshot(dr.ID)

	if snap1.Status == snap2.Status {
		t.Errorf("Status should differ: both are %s", snap1.Status)
	}
	// OpsKey should remain the same (no ops changed)
	if snap1.OpsKey != snap2.OpsKey {
		t.Errorf("OpsKey should be equal when only status changed\n  got1: %s\n  got2: %s", snap1.OpsKey, snap2.OpsKey)
	}

	// Cleanup
	svc.DB.Delete(&dr)
}

func TestCaptureDrSnapshot_EmptyOps(t *testing.T) {
	svc := newTestService(nil)

	dr := db.DeliveryRequest{
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "test",
		UpdatedBy:       "test",
	}
	svc.DB.Create(&dr)

	snap1 := svc.captureDrSnapshot(dr.ID)
	snap2 := svc.captureDrSnapshot(dr.ID)

	if snap1.OpsKey != snap2.OpsKey {
		t.Errorf("OpsKey for empty ops should be consistent\n  got1: %s\n  got2: %s", snap1.OpsKey, snap2.OpsKey)
	}

	// Add an op → OpsKey should change
	svc.DB.Create(&db.ArtifactTenantOperation{
		DeliveryRequestID: dr.ID,
		ImportState:       lifecycle.ImportNotStarted,
		DeployState:       lifecycle.DeployNotStarted,
	})
	snap3 := svc.captureDrSnapshot(dr.ID)

	if snap3.OpsKey == snap1.OpsKey {
		t.Errorf("OpsKey should differ after adding an op\n  before: %s\n  after:  %s", snap1.OpsKey, snap3.OpsKey)
	}

	// Cleanup
	svc.DB.Where("delivery_request_id = ?", dr.ID).Delete(&db.ArtifactTenantOperation{})
	svc.DB.Delete(&dr)
}

func TestCaptureDrSnapshot_NonExistentDR(t *testing.T) {
	svc := newTestService(nil)

	snap := svc.captureDrSnapshot(999999)
	if snap.Exists {
		t.Error("snapshot of non-existent DR should have Exists=false")
	}
}
