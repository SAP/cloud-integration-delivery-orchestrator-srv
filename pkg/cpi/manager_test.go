package cpi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

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

	database := newManagerTestDB(t)
	database.Create(&db.CpiTenant{Name: "t", PirApiDestinationName: "d", CfApiEndpoint: "x", CfOrg: "o"})
	var tenant db.CpiTenant
	database.First(&tenant)

	m := NewManager(database, resolver)

	done := make(chan struct{})
	go func() {
		_, _ = m.Get(context.Background(), tenant.ID)
		close(done)
	}()

	<-entered              // Get is parked building off-lock (version captured)
	m.Invalidate(tenant.ID) // bump gen during the build
	close(release)
	<-done

	m.mu.RLock()
	_, cached := m.clients[tenant.ID]
	m.mu.RUnlock()
	if cached {
		t.Fatal("stale client was cached after Invalidate during build")
	}
}

func newManagerTestDB(t *testing.T) *gorm.DB {
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
