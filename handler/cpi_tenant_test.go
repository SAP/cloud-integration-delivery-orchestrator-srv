package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// newTestHandler creates a Handler with an in-memory SQLite DB for use in handler tests.
func newTestHandler(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&mode=memory"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.AutoMigrate(&db.CpiTenant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := &Handler{db: database}
	return h, database
}

// postJSON fires a POST request with body against the given gin handler func and returns the recorder.
// It injects a minimal UaaClaims so that service.UserID() does not panic.
func postJSON(t *testing.T, handlerFunc gin.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Inject the UaaClaims that service.UserID() reads from context.
	c.Set("uaa_claim", db.UaaClaims{UserID: "test-user"})
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	c.Request, _ = http.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
	c.Request.Header.Set("Content-Type", "application/json")
	handlerFunc(c)
	return w
}

// newTestHandlerWithSvc creates a Handler with an in-memory SQLite DB and a
// real service.Service wired to the same DB, for tests that exercise paths
// that call h.svc (e.g. TransitionLifecycle).
func newTestHandlerWithSvc(t *testing.T) (*Handler, *gorm.DB) {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=private"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.AutoMigrate(&db.CpiTenant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := &service.Service{DB: database}
	h := &Handler{db: database, svc: svc}
	return h, database
}

// --- UpsertCpiTenant update path ---

// TestUpsertCpiTenant_Update_RejectsCfIdentityChange verifies that updating CF
// identity fields via UpsertCpiTenant returns 400; those fields must go through
// PUT /api/v1/cpiTenant/:id/cfIdentity.
func TestUpsertCpiTenant_Update_RejectsCfIdentityChange(t *testing.T) {
	h, database := newTestHandlerWithSvc(t)

	tenant := db.CpiTenant{
		Name:          "test-tenant",
		CfApiEndpoint: "https://api.cf.eu10.hana.ondemand.com",
		CfOrg:         "org-guid-abc",
		CfSpace:       "space-guid-xyz",
		LifecycleState: lifecycle.TenantConfigured,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() { database.Unscoped().Delete(&tenant) })

	update := tenant
	update.CfOrg = "org-guid-NEW"
	w := postJSON(t, h.UpsertCpiTenant, update)
	if w.Code != 400 {
		t.Errorf("expected 400 when changing CF identity via UpsertCpiTenant, got %d: %s", w.Code, w.Body.String())
	}
}

// --- UpsertCpiTenant create path ---

// TestUpsertCpiTenant_Create_RequiresName verifies that Name is required.
func TestUpsertCpiTenant_Create_RequiresName(t *testing.T) {
	h, database := newTestHandler(t)
	_ = database

	w := postJSON(t, h.UpsertCpiTenant, map[string]any{})
	if w.Code != 400 {
		t.Errorf("expected 400 for missing name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpsertCpiTenant_Create_SetsDraftState(t *testing.T) {
	h, database := newTestHandler(t)
	t.Cleanup(func() {
		database.Unscoped().Where("1=1").Delete(&db.CpiTenant{})
	})

	w := postJSON(t, h.UpsertCpiTenant, map[string]any{"name": "Tenant Alpha"})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data db.CpiTenant `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.LifecycleState != lifecycle.TenantDraft {
		t.Errorf("lifecycle_state = %q, want %q", resp.Data.LifecycleState, lifecycle.TenantDraft)
	}
}


