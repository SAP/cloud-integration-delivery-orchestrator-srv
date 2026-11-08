package cas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestNewManager_InitializesClientCache(t *testing.T) {
	manager := NewManager(nil, nil)
	if manager == nil {
		t.Fatal("expected manager instance, got nil")
	}
	if manager.clients == nil {
		t.Fatal("expected client cache to be initialized")
	}
	if len(manager.clients) != 0 {
		t.Fatalf("expected empty client cache, got %d entries", len(manager.clients))
	}
}

func TestManagerGet_ReturnsCachedClientWithoutResolver(t *testing.T) {
	manager := NewManager(nil, nil)
	cached := &CasClient{
		HttpClient: &env.HttpClient{
			ApiURL:      "https://cas.example.test",
			AccessToken: "token",
			TokenExp:    time.Now().Add(time.Hour),
		},
	}
	manager.clients[42] = cached

	got, err := manager.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("expected cached lookup to succeed, got %v", err)
	}
	if got != cached {
		t.Fatal("expected cached client instance to be returned")
	}
}

func TestManagerGet_ReportsMissingResolverOnCacheMiss(t *testing.T) {
	manager := NewManager(nil, nil)

	_, err := manager.Get(context.Background(), 99)
	if err == nil {
		t.Fatal("expected cache miss without resolver to fail")
	}
	if !strings.Contains(err.Error(), "DestinationServiceClient not injected") {
		t.Fatalf("expected missing resolver error, got %v", err)
	}
}

// If Invalidate runs while Get is building a client off-lock, the built client
// must NOT be cached (§5.3): a later Get rebuilds against the current binding.
func TestManagerGet_InvalidateDuringBuild_NoStaleCache(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var servedDest bool

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth/token"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
		case strings.Contains(r.URL.Path, "/subaccountDestinations/"):
			if !servedDest {
				servedDest = true
				close(entered)
				<-release // block the build mid-flight
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(cf.Destination{
				Name: "d", URL: server.URL, Authentication: "OAuth2ClientCredentials",
				TokenServiceURL: server.URL + "/oauth/token", ClientId: "cid", ClientSecret: "sec",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolver, err := cf.NewDestinationServiceClient(context.Background(), map[string]any{
		"clientid": "p", "clientsecret": "s", "url": server.URL, "uri": server.URL,
	})
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}

	database := newCasManagerTestDB(t)
	database.Create(&db.CpiTenant{Name: "t", CasEngineDestinationName: "d", CfApiEndpoint: "x", CfOrg: "o"})
	var tenant db.CpiTenant
	database.First(&tenant)

	m := NewManager(database, resolver)

	done := make(chan struct{})
	go func() {
		_, _ = m.Get(context.Background(), tenant.ID)
		close(done)
	}()

	<-entered
	m.Invalidate(tenant.ID)
	close(release)
	<-done

	m.mu.RLock()
	_, cached := m.clients[tenant.ID]
	m.mu.RUnlock()
	if cached {
		t.Fatal("stale client was cached after Invalidate during build")
	}
}

func newCasManagerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=private"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if sqlDB, err := d.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := d.AutoMigrate(&db.CpiTenant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}
