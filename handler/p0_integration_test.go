package handler

// p0_integration_test.go — RFC 032 P0 §6.1 handler-level test coverage.
//
// Rule 4 (Central TMS change invalidates bootstrapped tenants) and
// upsert whitelist enforcement.

import (
	"encoding/json"
	"net/http"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// newTestHandlerWithTms creates a handler with CpiTenant + CentralTmsContext migrated.
func newTestHandlerWithTms(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=private"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.AutoMigrate(&db.CpiTenant{}, &db.CentralTmsContext{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := &Handler{db: database, svc: &service.Service{DB: database}}
	return h, database
}

// ── §6.1: Rule 4 — UpsertCentralTmsContext invalidates bootstrapped tenants ──

func TestRule4_UpsertCentralTms_InvalidatesBootstrappedTenants(t *testing.T) {
	h, database := newTestHandlerWithTms(t)

	// Seed existing Central TMS context.
	existing := db.CentralTmsContext{TmsApiDestinationName: "old-tms-dest"}
	if err := database.Create(&existing).Error; err != nil {
		t.Fatalf("seed central TMS: %v", err)
	}

	// Seed tenants with various TMS states.
	bootstrapped1 := db.CpiTenant{
		Name: "tenant-boot1", LifecycleState: lifecycle.TenantReady,
		TmsExtensionState:             lifecycle.TmsBootstrapped,
		TransportManagementDestStatus: lifecycle.PrereqReady,
		CfApiEndpoint:                 "https://api.cf.eu10.example.com", CfOrg: "org-1", CfSpace: "space-1",
	}
	bootstrapped2 := db.CpiTenant{
		Name: "tenant-boot2", LifecycleState: lifecycle.TenantReady,
		TmsExtensionState:             lifecycle.TmsBootstrapped,
		TransportManagementDestStatus: lifecycle.PrereqReady,
		CfApiEndpoint:                 "https://api.cf.eu10.example.com", CfOrg: "org-2", CfSpace: "space-2",
	}
	notStarted := db.CpiTenant{
		Name: "tenant-ns", LifecycleState: lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsNotStarted,
		CfApiEndpoint:     "https://api.cf.eu10.example.com", CfOrg: "org-3", CfSpace: "space-3",
	}
	for _, tn := range []*db.CpiTenant{&bootstrapped1, &bootstrapped2, &notStarted} {
		if err := database.Create(tn).Error; err != nil {
			t.Fatalf("create tenant %q: %v", tn.Name, err)
		}
	}

	// Change the destination name.
	w := postJSON(t, h.UpsertCentralTmsContext, map[string]string{
		"TmsApiDestinationName": "new-tms-dest",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			TmsApiDestinationName string `json:"TmsApiDestinationName"`
			AffectedTenants       int64  `json:"affectedTenants"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.AffectedTenants != 2 {
		t.Errorf("affectedTenants = %d, want 2", resp.Data.AffectedTenants)
	}
	if resp.Data.TmsApiDestinationName != "new-tms-dest" {
		t.Errorf("TmsApiDestinationName = %q, want %q", resp.Data.TmsApiDestinationName, "new-tms-dest")
	}

	// Verify bootstrapped tenants are now not_started (Rule 4: Central TMS change
	// may point to different TMS system, old node IDs unsafe to keep).
	var t1, t2, t3 db.CpiTenant
	database.First(&t1, bootstrapped1.ID)
	database.First(&t2, bootstrapped2.ID)
	database.First(&t3, notStarted.ID)

	if t1.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("tenant1 TmsExtensionState = %q, want %q", t1.TmsExtensionState, lifecycle.TmsNotStarted)
	}
	if t1.TmsBlockingReason != lifecycle.TmsReasonCentralTmsChanged {
		t.Errorf("tenant1 TmsBlockingReason = %q, want %q", t1.TmsBlockingReason, lifecycle.TmsReasonCentralTmsChanged)
	}
	if t2.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("tenant2 TmsExtensionState = %q, want %q", t2.TmsExtensionState, lifecycle.TmsNotStarted)
	}
	// Rule 4 must also clear node fields.
	if t1.TmsSourceNodeName != "" {
		t.Errorf("tenant1 TmsSourceNodeName = %q, want empty", t1.TmsSourceNodeName)
	}
	if t1.TmsSourceNodeID != 0 {
		t.Errorf("tenant1 TmsSourceNodeID = %d, want 0", t1.TmsSourceNodeID)
	}
	// not_started tenant should be unaffected.
	if t3.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("tenant3 TmsExtensionState = %q, want %q (should be unaffected)", t3.TmsExtensionState, lifecycle.TmsNotStarted)
	}
}

func TestRule4_UpsertCentralTms_RejectsWhenBootstrapping(t *testing.T) {
	h, database := newTestHandlerWithTms(t)

	existing := db.CentralTmsContext{TmsApiDestinationName: "old-dest"}
	database.Create(&existing)

	// Seed a tenant that is currently bootstrapping TMS extension.
	bootstrapping := db.CpiTenant{
		Name: "tenant-ing", LifecycleState: lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapping,
	}
	database.Create(&bootstrapping)

	w := postJSON(t, h.UpsertCentralTmsContext, map[string]string{
		"TmsApiDestinationName": "changed-dest",
	})

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

func TestRule4_UpsertCentralTms_NoChangeNoInvalidation(t *testing.T) {
	h, database := newTestHandlerWithTms(t)

	existing := db.CentralTmsContext{TmsApiDestinationName: "same-dest"}
	database.Create(&existing)

	bootstrapped := db.CpiTenant{
		Name: "tenant-stable", LifecycleState: lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapped,
	}
	database.Create(&bootstrapped)

	// PUT with the SAME destination name — should NOT invalidate.
	w := postJSON(t, h.UpsertCentralTmsContext, map[string]string{
		"TmsApiDestinationName": "same-dest",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var got db.CpiTenant
	database.First(&got, bootstrapped.ID)
	if got.TmsExtensionState != lifecycle.TmsBootstrapped {
		t.Errorf("TmsExtensionState = %q, want %q (no change = no invalidation)",
			got.TmsExtensionState, lifecycle.TmsBootstrapped)
	}
}

// ── §6.1: Upsert whitelist — TMS fields in request body are ignored ─────────

func TestUpsertCpiTenant_Update_IgnoresTmsFields(t *testing.T) {
	h, database := newTestHandler(t)

	// Seed a tenant with known TMS state.
	tenant := db.CpiTenant{
		Name:                  "whitelist-test",
		LifecycleState:        lifecycle.TenantReady,
		TmsExtensionState:     lifecycle.TmsBootstrapped,
		TmsSourceNodeName:     "real-node",
		TmsSourceNodeID:       42,
		PirApiDestinationName: "PIR_DEST",
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// Try to update with TMS fields in the body — they should be ignored.
	body := map[string]any{
		"ID":                    tenant.ID,
		"Name":                  "whitelist-test-renamed",
		"TmsExtensionState":     "not_started",
		"TmsSourceNodeName":     "hacked-node",
		"TmsSourceNodeID":       999,
		"PirApiDestinationName": "HACKED_PIR",
		"LifecycleState":        "draft",
		"CfApiEndpoint":         "",
		"CfOrg":                 "",
		"CfSpace":               "",
	}

	w := postJSON(t, h.UpsertCpiTenant, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var got db.CpiTenant
	database.First(&got, tenant.ID)

	// Name should be updated (whitelist field).
	if got.Name != "whitelist-test-renamed" {
		t.Errorf("Name = %q, want %q", got.Name, "whitelist-test-renamed")
	}
	// Protected fields should be unchanged.
	if got.TmsExtensionState != lifecycle.TmsBootstrapped {
		t.Errorf("TmsExtensionState = %q, want %q (should be ignored)", got.TmsExtensionState, lifecycle.TmsBootstrapped)
	}
	if got.TmsSourceNodeName != "real-node" {
		t.Errorf("TmsSourceNodeName = %q, want %q (should be ignored)", got.TmsSourceNodeName, "real-node")
	}
	if got.TmsSourceNodeID != 42 {
		t.Errorf("TmsSourceNodeID = %d, want %d (should be ignored)", got.TmsSourceNodeID, 42)
	}
	if got.PirApiDestinationName != "PIR_DEST" {
		t.Errorf("PirApiDestinationName = %q, want %q (should be ignored)", got.PirApiDestinationName, "PIR_DEST")
	}
	if got.LifecycleState != lifecycle.TenantReady {
		t.Errorf("LifecycleState = %q, want %q (should be ignored)", got.LifecycleState, lifecycle.TenantReady)
	}
}
