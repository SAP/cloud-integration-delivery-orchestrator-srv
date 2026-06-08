package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/lifecycle"
)

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
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "test-token",
				"expires_in":   3600,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/destination-configuration/v1/subaccountDestinations/"+destinationName:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(cf.Destination{
				Name:            destinationName,
				URL:             server.URL,
				Authentication:  "OAuth2ClientCredentials",
				TokenServiceURL: server.URL + "/oauth/token",
				ClientId:        "tms-client",
				ClientSecret:    "tms-secret",
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/destination-configuration/v1/subaccountDestinations/"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/routes":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"routes": routes,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := cf.NewDestinationServiceClient(context.Background(), map[string]any{
		"clientid":     "provider-client",
		"clientsecret": "provider-secret",
		"url":          server.URL,
		"uri":          server.URL,
	})
	if err != nil {
		t.Fatalf("create provider destination client: %v", err)
	}
	return client
}

func TestRegisterTmsNode_SetsRegisteringAndStoresNode(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "register-node-"+t.Name())

	svc := newTestService(nil)
	if err := svc.RegisterTmsNode(context.Background(), tenant.ID, 321, "NODE-321"); err != nil {
		t.Fatalf("RegisterTmsNode failed: %v", err)
	}

	var updated db.CpiTenant
	if err := testDB.First(&updated, tenant.ID).Error; err != nil {
		t.Fatalf("reload tenant: %v", err)
	}
	if updated.TmsNodeRegistrationStatus != lifecycle.PrereqRegistering {
		t.Fatalf("status = %s, want %s", updated.TmsNodeRegistrationStatus, lifecycle.PrereqRegistering)
	}
	if updated.TmsSourceNodeID != 321 || updated.TmsSourceNodeName != "NODE-321" {
		t.Fatalf("unexpected node binding: id=%d name=%q", updated.TmsSourceNodeID, updated.TmsSourceNodeName)
	}
}

func TestRegisterTmsNode_RejectsAlreadyRegistering(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "register-busy-"+t.Name())
	tenant.TmsNodeRegistrationStatus = lifecycle.PrereqRegistering
	tenant.TmsSourceNodeID = 111
	tenant.TmsSourceNodeName = "OLD-NODE"
	if err := testDB.Save(&tenant).Error; err != nil {
		t.Fatalf("save tenant: %v", err)
	}

	svc := newTestService(nil)
	err := svc.RegisterTmsNode(context.Background(), tenant.ID, 222, "NEW-NODE")
	if !errors.Is(err, ErrAlreadyRegistering) {
		t.Fatalf("expected ErrAlreadyRegistering, got %v", err)
	}

	var unchanged db.CpiTenant
	if err := testDB.First(&unchanged, tenant.ID).Error; err != nil {
		t.Fatalf("reload tenant: %v", err)
	}
	if unchanged.TmsSourceNodeID != 111 || unchanged.TmsSourceNodeName != "OLD-NODE" {
		t.Fatalf("node binding changed unexpectedly: id=%d name=%q", unchanged.TmsSourceNodeID, unchanged.TmsSourceNodeName)
	}
}

func TestConfirmTmsRoutes_ValidatesRoutesAndPersistsReadyState(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "confirm-routes-"+t.Name())
	tenant.TmsNodeRegistrationStatus = lifecycle.PrereqRegistering
	if err := testDB.Save(&tenant).Error; err != nil {
		t.Fatalf("save tenant: %v", err)
	}

	t.Run("node ID required", func(t *testing.T) {
		svc := newTestService(nil)
		if _, err := svc.ConfirmTmsRoutes(context.Background(), tenant.ID, 0); err == nil || !strings.Contains(err.Error(), "nodeID is required") {
			t.Fatalf("expected nodeID required error, got %v", err)
		}
	})

	t.Run("empty routes keep tenant registering", func(t *testing.T) {
		ctx := seedCentralTmsContext(t, "tms-api-empty")
		svc := newTestService(nil)
		svc.ProviderDest = newProviderDestForRoutes(t, ctx.TmsApiDestinationName, nil)

		_, err := svc.ConfirmTmsRoutes(context.Background(), tenant.ID, 200)
		if !errors.Is(err, ErrRoutesNotConfigured) {
			t.Fatalf("expected ErrRoutesNotConfigured, got %v", err)
		}

		var unchanged db.CpiTenant
		if err := testDB.First(&unchanged, tenant.ID).Error; err != nil {
			t.Fatalf("reload tenant: %v", err)
		}
		if unchanged.TmsNodeRegistrationStatus != lifecycle.PrereqRegistering {
			t.Fatalf("status = %s, want %s", unchanged.TmsNodeRegistrationStatus, lifecycle.PrereqRegistering)
		}
	})

	t.Run("routes found persist ready state and context ID", func(t *testing.T) {
		ctx := seedCentralTmsContext(t, "tms-api-ready")
		svc := newTestService(nil)
		svc.ProviderDest = newProviderDestForRoutes(t, ctx.TmsApiDestinationName, []db.TransportRoute{
			{ID: 1, SourceNodeID: 200, TargetNodeID: 201, Name: "src-route"},
			{ID: 2, SourceNodeID: 300, TargetNodeID: 200, Name: "target-route"},
			{ID: 3, SourceNodeID: 999, TargetNodeID: 998, Name: "other-route"},
		})

		result, err := svc.ConfirmTmsRoutes(context.Background(), tenant.ID, 200)
		if err != nil {
			t.Fatalf("ConfirmTmsRoutes failed: %v", err)
		}
		if len(result.Routes) != 2 {
			t.Fatalf("expected 2 relevant routes, got %d", len(result.Routes))
		}

		var updated db.CpiTenant
		if err := testDB.First(&updated, tenant.ID).Error; err != nil {
			t.Fatalf("reload tenant: %v", err)
		}
		if updated.TmsNodeRegistrationStatus != lifecycle.PrereqReady {
			t.Fatalf("status = %s, want %s", updated.TmsNodeRegistrationStatus, lifecycle.PrereqReady)
		}
		if updated.CentralTmsContextID == nil || *updated.CentralTmsContextID != ctx.ID {
			t.Fatalf("central context id = %v, want %d", updated.CentralTmsContextID, ctx.ID)
		}
	})
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
