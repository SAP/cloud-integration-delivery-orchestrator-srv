package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestTmsTransitions_AllValidEdges verifies every edge in the allowedTmsTransitions table.
func TestTmsTransitions_AllValidEdges(t *testing.T) {
	tests := []struct {
		name      string
		from      lifecycle.TmsExtensionState
		event     TmsEvent
		wantState lifecycle.TmsExtensionState
	}{
		// not_started
		{"not_started → node_selected → node_confirmed", lifecycle.TmsNotStarted, TmsEventNodeSelected, lifecycle.TmsNodeConfirmed},
		{"not_started → reset → not_started", lifecycle.TmsNotStarted, TmsEventReset, lifecycle.TmsNotStarted},
		// node_confirmed
		{"node_confirmed → node_selected → node_confirmed", lifecycle.TmsNodeConfirmed, TmsEventNodeSelected, lifecycle.TmsNodeConfirmed},
		{"node_confirmed → apply_started → bootstrapping", lifecycle.TmsNodeConfirmed, TmsEventApplyStarted, lifecycle.TmsBootstrapping},
		{"node_confirmed → reset → not_started", lifecycle.TmsNodeConfirmed, TmsEventReset, lifecycle.TmsNotStarted},
		// bootstrapping
		{"bootstrapping → apply_finished → bootstrapped", lifecycle.TmsBootstrapping, TmsEventApplyFinished, lifecycle.TmsBootstrapped},
		{"bootstrapping → apply_failed → node_confirmed", lifecycle.TmsBootstrapping, TmsEventApplyFailed, lifecycle.TmsNodeConfirmed},
		// bootstrapped
		{"bootstrapped → node_selected → node_confirmed", lifecycle.TmsBootstrapped, TmsEventNodeSelected, lifecycle.TmsNodeConfirmed},
		{"bootstrapped → apply_started → bootstrapping", lifecycle.TmsBootstrapped, TmsEventApplyStarted, lifecycle.TmsBootstrapping},
		{"bootstrapped → invalidated → node_confirmed", lifecycle.TmsBootstrapped, TmsEventInvalidated, lifecycle.TmsNodeConfirmed},
		{"bootstrapped → reset → not_started", lifecycle.TmsBootstrapped, TmsEventReset, lifecycle.TmsNotStarted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, database := newTestSvc(t)
			tenant := db.CpiTenant{
				Name:           tt.name,
				CfApiEndpoint:  "https://test",
				CfOrg:          tt.name,
				LifecycleState: lifecycle.TenantReady,
				TmsExtensionState: tt.from,
			}
			if err := database.Create(&tenant).Error; err != nil {
				t.Fatalf("create: %v", err)
			}

			if err := svc.TransitionTms(tenant.ID, tt.event); err != nil {
				t.Fatalf("TransitionTms(%q, %q): %v", tt.from, tt.event, err)
			}

			var got db.CpiTenant
			if err := database.First(&got, tenant.ID).Error; err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.TmsExtensionState != tt.wantState {
				t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, tt.wantState)
			}
		})
	}
}

// TestTmsTransitions_InvalidEdgesRejected verifies that transitions not in the
// table are rejected with ErrTmsTransitionNotAllowed.
func TestTmsTransitions_InvalidEdgesRejected(t *testing.T) {
	tests := []struct {
		name  string
		from  lifecycle.TmsExtensionState
		event TmsEvent
	}{
		// not_started: cannot apply, finish, fail, or invalidate
		{"not_started × apply_started", lifecycle.TmsNotStarted, TmsEventApplyStarted},
		{"not_started × apply_finished", lifecycle.TmsNotStarted, TmsEventApplyFinished},
		{"not_started × apply_failed", lifecycle.TmsNotStarted, TmsEventApplyFailed},
		{"not_started × invalidated", lifecycle.TmsNotStarted, TmsEventInvalidated},
		// node_confirmed: cannot finish, fail, or invalidate
		{"node_confirmed × apply_finished", lifecycle.TmsNodeConfirmed, TmsEventApplyFinished},
		{"node_confirmed × apply_failed", lifecycle.TmsNodeConfirmed, TmsEventApplyFailed},
		{"node_confirmed × invalidated", lifecycle.TmsNodeConfirmed, TmsEventInvalidated},
		// bootstrapping: cannot select node, apply, invalidate, or reset
		{"bootstrapping × node_selected", lifecycle.TmsBootstrapping, TmsEventNodeSelected},
		{"bootstrapping × apply_started", lifecycle.TmsBootstrapping, TmsEventApplyStarted},
		{"bootstrapping × invalidated", lifecycle.TmsBootstrapping, TmsEventInvalidated},
		{"bootstrapping × reset", lifecycle.TmsBootstrapping, TmsEventReset},
		// bootstrapped: cannot finish or fail
		{"bootstrapped × apply_finished", lifecycle.TmsBootstrapped, TmsEventApplyFinished},
		{"bootstrapped × apply_failed", lifecycle.TmsBootstrapped, TmsEventApplyFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, database := newTestSvc(t)
			tenant := db.CpiTenant{
				Name:           tt.name,
				CfApiEndpoint:  "https://test",
				CfOrg:          tt.name,
				LifecycleState: lifecycle.TenantReady,
				TmsExtensionState: tt.from,
			}
			if err := database.Create(&tenant).Error; err != nil {
				t.Fatalf("create: %v", err)
			}

			err := svc.TransitionTms(tenant.ID, tt.event)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errorContains(err, ErrTmsTransitionNotAllowed.Error()) {
				t.Errorf("error = %v, want containing %q", err, ErrTmsTransitionNotAllowed.Error())
			}

			// State must be unchanged.
			var got db.CpiTenant
			if err := database.First(&got, tenant.ID).Error; err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.TmsExtensionState != tt.from {
				t.Errorf("TmsExtensionState changed to %q, want unchanged %q", got.TmsExtensionState, tt.from)
			}
		})
	}
}

// TestTmsTransitions_BootstrappingBlocksReset verifies that bootstrapping state blocks reset
// (rule 1 cannot fire while TMS bootstrap is running).
func TestTmsTransitions_BootstrappingBlocksReset(t *testing.T) {
	svc, database := newTestSvc(t)
	tenant := db.CpiTenant{
		Name:              "bootstrapping-blocks-reset",
		CfApiEndpoint:     "https://test",
		CfOrg:             "bootstrapping-reset",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapping,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	err := svc.TransitionTms(tenant.ID, TmsEventReset)
	if err == nil {
		t.Fatal("expected error for reset from bootstrapping, got nil")
	}
}

// newTestSvc creates a Service with a per-test in-memory SQLite DB.
func newTestSvc(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	database := newTestDB(t)
	svc := &Service{DB: database}
	return svc, database
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=private"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.AutoMigrate(&db.CpiTenant{}, &db.TenantBootstrapJob{}, &db.CentralTmsContext{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func errorContains(err error, substr string) bool {
	return err != nil && strings.Contains(err.Error(), substr)
}

// ── Route query helpers & tests (merged from tms_node_registrar_test.go) ─────

func seedCentralTmsContext(t *testing.T, destinationName string) db.CentralTmsContext {
	t.Helper()
	if err := testDB.Unscoped().Where("1 = 1").Delete(&db.CentralTmsContext{}).Error; err != nil {
		t.Fatalf("clear central TMS context: %v", err)
	}
	t.Cleanup(func() {
		_ = testDB.Unscoped().Where("1 = 1").Delete(&db.CentralTmsContext{}).Error
	})
	ctx := db.CentralTmsContext{TmsApiDestinationName: destinationName}
	if err := testDB.Create(&ctx).Error; err != nil {
		t.Fatalf("seed central TMS context: %v", err)
	}
	return ctx
}

func newProviderDestForRoutes(t *testing.T, destinationName string, routes []db.TransportRoute) *cf.DestinationServiceClient {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "expires_in": 3600})
		case r.Method == http.MethodGet && r.URL.Path == "/destination-configuration/v1/subaccountDestinations/"+destinationName:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(cf.Destination{
				Name: destinationName, URL: server.URL, Authentication: "OAuth2ClientCredentials",
				TokenServiceURL: server.URL + "/oauth/token", ClientId: "tms-client", ClientSecret: "tms-secret",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v2/routes":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"routes": routes})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := cf.NewDestinationServiceClient(context.Background(), map[string]any{
		"clientid": "provider-client", "clientsecret": "provider-secret",
		"url": server.URL, "uri": server.URL,
	})
	if err != nil {
		t.Fatalf("create provider destination client: %v", err)
	}
	return client
}

func TestGetCurrentNodeRoutes_RequiresRegistrationAndReturnsFilteredRoutes(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "current-routes-"+t.Name())

	svc := newTestService(nil)
	_, err := svc.GetCurrentNodeRoutes(context.Background(), tenant.ID)
	if !errors.Is(err, ErrNodeNotRegistered) {
		t.Fatalf("expected ErrNodeNotRegistered, got %v", err)
	}

	tenant.TmsSourceNodeID = 400
	tenant.TmsSourceNodeName = "NODE-400"
	if err := testDB.Save(&tenant).Error; err != nil {
		t.Fatalf("save tenant: %v", err)
	}

	ctx := seedCentralTmsContext(t, "tms-api-routes")
	svc.ProviderDest = newProviderDestForRoutes(t, ctx.TmsApiDestinationName, []db.TransportRoute{
		{ID: 10, SourceNodeID: 400, TargetNodeID: 401, Name: "outbound"},
		{ID: 11, SourceNodeID: 402, TargetNodeID: 400, Name: "inbound"},
		{ID: 12, SourceNodeID: 500, TargetNodeID: 501, Name: "unrelated"},
	})

	result, err := svc.GetCurrentNodeRoutes(context.Background(), tenant.ID)
	if err != nil {
		t.Fatalf("GetCurrentNodeRoutes failed: %v", err)
	}
	if result.NodeName != tenant.TmsSourceNodeName {
		t.Fatalf("node name = %q, want %q", result.NodeName, tenant.TmsSourceNodeName)
	}
	if len(result.Routes) != 2 {
		t.Fatalf("expected 2 filtered routes, got %d", len(result.Routes))
	}
}
