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

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/service"
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

// --- UpsertCpiTenant update path — key field change / state machine ---

// TestUpsertCpiTenant_Update_KeyFieldChange_Readying verifies that modifying a
// key CF identity field while bootstrap is in progress (TenantReadying) is
// rejected with 409.
func TestUpsertCpiTenant_Update_KeyFieldChange_Readying(t *testing.T) {
	h, database := newTestHandlerWithSvc(t)

	tenant := db.CpiTenant{
		Name:           "test-tenant",
		CfApiEndpoint:  "https://api.cf.eu10.hana.ondemand.com",
		CfOrg:          "org-guid-abc",
		CfSpace:        "space-guid-xyz",
		LifecycleState: lifecycle.TenantReadying,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Attempt to change CfOrg while bootstrap is running.
	update := tenant
	update.CfOrg = "org-guid-NEW"
	w := postJSON(t, h.UpsertCpiTenant, update)
	if w.Code != 409 {
		t.Errorf("expected 409 when changing key field during readying, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpsertCpiTenant_Update_KeyFieldChange_ResetsToMraft verifies that modifying
// a key CF identity field from a non-readying state transitions the tenant back
// to DRAFT and clears prerequisite statuses.
func TestUpsertCpiTenant_Update_KeyFieldChange_ResetsToMraft(t *testing.T) {
	h, database := newTestHandlerWithSvc(t)

	tenant := db.CpiTenant{
		Name:                  "test-tenant",
		CfApiEndpoint:         "https://api.cf.eu10.hana.ondemand.com",
		CfOrg:                 "org-guid-abc",
		CfSpace:               "space-guid-xyz",
		LifecycleState:        lifecycle.TenantNotReady,
		PirApiStatus:          lifecycle.PrereqReady,
		CasApplicationStatus:  lifecycle.PrereqReady,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	update := tenant
	update.CfOrg = "org-guid-NEW"
	w := postJSON(t, h.UpsertCpiTenant, update)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var updated db.CpiTenant
	database.First(&updated, tenant.ID)
	if updated.LifecycleState != lifecycle.TenantDraft {
		t.Errorf("lifecycle_state = %q, want %q", updated.LifecycleState, lifecycle.TenantDraft)
	}
	if updated.PirApiStatus != lifecycle.PrereqMissing {
		t.Errorf("PirApiStatus = %q, want %q", updated.PirApiStatus, lifecycle.PrereqMissing)
	}
}

func TestKeyFieldChanged(t *testing.T) {
	base := db.CpiTenant{
		CfApiEndpoint: "https://api.cf.eu10.hana.ondemand.com",
		CfOrg:         "org-guid-abc",
		CfSpace:       "space-guid-xyz",
	}

	cases := []struct {
		name    string
		input   db.CpiTenant
		changed bool
	}{
		{
			"no change",
			base,
			false,
		},
		{
			"CfApiEndpoint changed",
			db.CpiTenant{CfApiEndpoint: "https://api.cf.eu20.hana.ondemand.com", CfOrg: base.CfOrg, CfSpace: base.CfSpace},
			true,
		},
		{
			"CfOrg changed",
			db.CpiTenant{CfApiEndpoint: base.CfApiEndpoint, CfOrg: "org-guid-NEW", CfSpace: base.CfSpace},
			true,
		},
		{
			"CfSpace changed",
			db.CpiTenant{CfApiEndpoint: base.CfApiEndpoint, CfOrg: base.CfOrg, CfSpace: "space-guid-NEW"},
			true,
		},
		{
			"all three changed",
			db.CpiTenant{CfApiEndpoint: "x", CfOrg: "y", CfSpace: "z"},
			true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keyFieldChanged(base, c.input)
			if got != c.changed {
				t.Errorf("keyFieldChanged = %v, want %v", got, c.changed)
			}
		})
	}
}

// --- UpsertCpiTenant create path ---

func TestUpsertCpiTenant_Create_RequiresFields(t *testing.T) {
	h, database := newTestHandler(t)

	cases := []struct {
		name           string
		body           map[string]any
		wantStatusCode int
	}{
		{
			"missing both fields returns 400",
			map[string]any{"name": "Tenant A"},
			400,
		},
		{
			"missing CfOrg returns 400",
			map[string]any{"name": "Tenant A", "cfApiEndpoint": "https://api.cf.eu10.hana.ondemand.com"},
			400,
		},
		{
			"missing CfApiEndpoint returns 400",
			map[string]any{"name": "Tenant A", "cfOrg": "org-guid"},
			400,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := postJSON(t, h.UpsertCpiTenant, c.body)
			if w.Code != c.wantStatusCode {
				t.Errorf("status = %d, want %d; body = %s", w.Code, c.wantStatusCode, w.Body.String())
			}
		})
	}

	_ = database // prevent unused warning; used via h.db
}

func TestUpsertCpiTenant_Create_SetsDraftState(t *testing.T) {
	h, database := newTestHandler(t)
	t.Cleanup(func() {
		database.Unscoped().Where("1=1").Delete(&db.CpiTenant{})
	})

	body := map[string]any{
		"name":          "Tenant Alpha",
		"cfApiEndpoint": "https://api.cf.eu10.hana.ondemand.com",
		"cfOrg":         "org-guid-alpha",
	}
	w := postJSON(t, h.UpsertCpiTenant, body)
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

func TestUpsertCpiTenant_Create_RejectsDuplicateCfIdentity(t *testing.T) {
	h, database := newTestHandler(t)
	t.Cleanup(func() {
		database.Unscoped().Where("1=1").Delete(&db.CpiTenant{})
	})

	body := map[string]any{
		"name":          "Tenant Beta",
		"cfApiEndpoint": "https://api.cf.eu10.hana.ondemand.com",
		"cfOrg":         "org-guid-beta",
	}

	// First create should succeed
	w1 := postJSON(t, h.UpsertCpiTenant, body)
	if w1.Code != 200 {
		t.Fatalf("first create: expected 200, got %d: %s", w1.Code, w1.Body.String())
	}

	// Second create with same (CfApiEndpoint, CfOrg) should be 409
	w2 := postJSON(t, h.UpsertCpiTenant, body)
	if w2.Code != 409 {
		t.Errorf("duplicate create: expected 409, got %d: %s", w2.Code, w2.Body.String())
	}
}
