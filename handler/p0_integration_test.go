package handler

// p0_integration_test.go — RFC 032 P0 §6.1 handler-level test coverage.
//
// Rule 4 (Central TMS change invalidates bootstrapped tenants) and
// upsert whitelist enforcement.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/tms"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// handlerMockTmsProvider satisfies service.TmsProvider for handler-level tests
// that exercise UpsertCentralTmsContext. Reconfigure runs the commit callback so
// the handler can test the DB-level Rule 4 logic.
type handlerMockTmsProvider struct{}

func (m *handlerMockTmsProvider) Get(_ context.Context) (tms.TransportService, uint64, error) {
	return nil, 0, fmt.Errorf("not configured in handler test")
}
func (m *handlerMockTmsProvider) Destination(_ context.Context) (cf.Destination, error) {
	return cf.Destination{}, fmt.Errorf("not configured in handler test")
}
func (m *handlerMockTmsProvider) RuntimeInfo(_ context.Context) (tms.RuntimeInfo, error) {
	return tms.RuntimeInfo{}, fmt.Errorf("not configured in handler test")
}
func (m *handlerMockTmsProvider) Reconfigure(_ context.Context, destName string, commit func(cf.Destination, time.Time) (uint64, error)) (tms.RuntimeInfo, error) {
	if _, err := commit(cf.Destination{Name: destName}, time.Now()); err != nil {
		return tms.RuntimeInfo{}, err
	}
	return tms.RuntimeInfo{Loaded: true, DestinationName: destName}, nil
}

// newTestHandlerWithTms creates a handler with CpiTenant + CentralTmsContext migrated
// and a mock TMS provider that supports Reconfigure for Rule 4 tests.
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
	h := &Handler{db: database, svc: &service.Service{
		DB:  database,
		TMS: &handlerMockTmsProvider{},
	}}
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

func TestRule4_UpsertCentralTms_SameDestStillInvalidates(t *testing.T) {
	h, database := newTestHandlerWithTms(t)

	existing := db.CentralTmsContext{TmsApiDestinationName: "same-dest"}
	database.Create(&existing)

	bootstrapped := db.CpiTenant{
		Name: "tenant-stable", LifecycleState: lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapped,
		CfApiEndpoint:     "https://api.cf.eu10.example.com", CfOrg: "org-stable",
	}
	database.Create(&bootstrapped)

	// PUT with the SAME destination name — Rule 4 still fires (RFC 033 §6.1:
	// every call triggers full Reconfigure + Rule 4, not just on name change).
	w := postJSON(t, h.UpsertCentralTmsContext, map[string]string{
		"TmsApiDestinationName": "same-dest",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var got db.CpiTenant
	database.First(&got, bootstrapped.ID)
	if got.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("TmsExtensionState = %q, want %q (same-dest still triggers Rule 4)",
			got.TmsExtensionState, lifecycle.TmsNotStarted)
	}
}

// ── §6.1: Same-name same-content PUT triggers Rule 4 + advances generation ───

// TestRule4_UpsertCentralTms_SameNameSameContent_AdvancesGenerationAndClearsNode
// demonstrates the core §6.1 rule: every call to UpsertCentralTmsContext is a
// full Reconfigure that runs Rule 4 and advances the config generation — even
// when the destination name AND content are identical to what is already stored.
// This ensures that same-name credential rotation or any silent content change
// also resets tenant node selections (which point to the now-potentially-changed
// TMS system). The generation advance is the observable proof that a new logical
// version was committed, not a no-op.
func TestRule4_UpsertCentralTms_SameNameSameContent_AdvancesGenerationAndClearsNode(t *testing.T) {
	h, database := newTestHandlerWithTms(t)

	// Seed Central TMS context with the destination name we will re-submit.
	if err := database.Create(&db.CentralTmsContext{TmsApiDestinationName: "same-dest"}).Error; err != nil {
		t.Fatalf("seed CentralTmsContext: %v", err)
	}

	// Seed a bootstrapped tenant with a registered node.
	bootstrapped := db.CpiTenant{
		Name:              "tenant-same-content",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapped,
		TmsSourceNodeName: "my-node",
		TmsSourceNodeID:   42,
		CfApiEndpoint:     "https://api.cf.eu10.example.com",
		CfOrg:             "org-same",
	}
	if err := database.Create(&bootstrapped).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// PUT with the EXACT SAME destination name (same content too — no change).
	// Rule 4 must still fire: every save is a full Reconfigure (RFC 033 §6.1).
	w := postJSON(t, h.UpsertCentralTmsContext, map[string]string{
		"TmsApiDestinationName": "same-dest",
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
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.AffectedTenants != 1 {
		t.Errorf("affectedTenants = %d, want 1", resp.Data.AffectedTenants)
	}

	// Tenant must be reset: state → not_started, node fields cleared.
	var got db.CpiTenant
	database.First(&got, bootstrapped.ID)
	if got.TmsExtensionState != lifecycle.TmsNotStarted {
		t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, lifecycle.TmsNotStarted)
	}
	if got.TmsSourceNodeName != "" {
		t.Errorf("TmsSourceNodeName = %q, want empty", got.TmsSourceNodeName)
	}
	if got.TmsSourceNodeID != 0 {
		t.Errorf("TmsSourceNodeID = %d, want 0", got.TmsSourceNodeID)
	}

	// Config generation must have advanced from 0 → 1 (a new logical version was
	// committed, not a no-op). LastValidatedAt must be persisted in the same tx.
	var savedCfg db.CentralTmsContext
	database.First(&savedCfg)
	if savedCfg.Generation != 1 {
		t.Errorf("Generation = %d, want 1 (same-content PUT still commits a new version)", savedCfg.Generation)
	}
	if savedCfg.LastValidatedAt == nil {
		t.Error("LastValidatedAt was not persisted by same-content Reconfigure")
	}
}

// ── R3: GetCentralTmsContext fallback behaviour ────────────────────────────────

func TestGetCentralTmsContext_ColdLoadFail_ReturnsDBRow(t *testing.T) {
	// RuntimeInfo fails (cold-load failure), but a config row exists in the DB.
	// The handler should return 200 with Loaded=false and the persisted name,
	// not a misleading 404 "not configured".
	h, database := newTestHandlerWithTms(t)

	now := time.Now()
	database.Create(&db.CentralTmsContext{
		Model:                 gorm.Model{ID: 1},
		TmsApiDestinationName: "COLD_FAIL_DEST",
		LastValidatedAt:       &now,
	})

	w := doGET(t, h.GetCentralTmsContext)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data tms.RuntimeInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	info := envelope.Data
	if info.Loaded {
		t.Error("Loaded = true, want false (cold-load failed)")
	}
	if info.DestinationName != "COLD_FAIL_DEST" {
		t.Errorf("DestinationName = %q, want %q", info.DestinationName, "COLD_FAIL_DEST")
	}
	if info.LastValidatedAt == nil {
		t.Error("LastValidatedAt should be returned from DB")
	}
}

func TestGetCentralTmsContext_NeverConfigured_Returns404(t *testing.T) {
	// No config row in the DB at all → genuine 404.
	h, _ := newTestHandlerWithTms(t)

	w := doGET(t, h.GetCentralTmsContext)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
}

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
