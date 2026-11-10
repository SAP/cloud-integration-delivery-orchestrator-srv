package cpi

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

// ── §4 error matrix (RFC 033 §4.3) ──────────────────────────────────────────
//
// CPI Manager.Get must return clear errors for every bad-binding scenario and
// must never panic. Tests cover: tenant not found, empty destination name,
// destination not found in BTP (404 → nil,nil), missing OAuth credentials.

// TestManagerGet_TenantNotFound verifies that Get wraps gorm.ErrRecordNotFound
// when the tenant row does not exist.
func TestManagerGet_TenantNotFound(t *testing.T) {
	database := newManagerTestDB(t)
	m := NewManager(database, nil) // resolver never reached
	_, err := m.Get(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for missing tenant, got nil")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("error = %v; want to wrap gorm.ErrRecordNotFound", err)
	}
}

// TestManagerGet_EmptyDestinationName verifies that Get returns ErrNoDestination
// when the tenant's PirApiDestinationName is empty.
func TestManagerGet_EmptyDestinationName(t *testing.T) {
	database := newManagerTestDB(t)
	tenant := db.CpiTenant{Name: "no-dest", PirApiDestinationName: "", CfApiEndpoint: "x", CfOrg: "o"}
	database.Create(&tenant)

	m := NewManager(database, nil) // resolver never reached for empty name
	_, err := m.Get(context.Background(), tenant.ID)
	if err == nil {
		t.Fatal("expected ErrNoDestination, got nil")
	}
	if !errors.Is(err, ErrNoDestination) {
		t.Errorf("error = %v; want to wrap ErrNoDestination", err)
	}
}

// TestManagerGet_DestinationNotFound verifies that Get returns an error (no panic)
// when the BTP Destination Service returns 404 for the tenant's destination name.
// cf.GetDestination maps a 404 to (nil, nil); the manager must handle it.
func TestManagerGet_DestinationNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if strings.Contains(r.URL.Path, "/subaccountDestinations/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	resolver, err := cf.NewDestinationServiceClient(context.Background(), map[string]any{
		"clientid": "p", "clientsecret": "s", "url": server.URL, "uri": server.URL,
	})
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}

	database := newManagerTestDB(t)
	tenant := db.CpiTenant{Name: "t", PirApiDestinationName: "missing-dest", CfApiEndpoint: "x", CfOrg: "o"}
	database.Create(&tenant)

	m := NewManager(database, resolver)
	_, err = m.Get(context.Background(), tenant.ID)
	if err == nil {
		t.Fatal("expected error for missing destination, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q; want to contain %q", err.Error(), "not found")
	}
}

// TestManagerGet_MissingOAuthCredentials verifies that Get returns an error (no panic)
// when the resolved destination has empty OAuth credentials.
func TestManagerGet_MissingOAuthCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if strings.Contains(r.URL.Path, "/subaccountDestinations/") {
			// Return a destination with no OAuth credentials.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(cf.Destination{
				Name: "no-creds-dest", URL: "https://cpi.example.com",
				Authentication: "OAuth2ClientCredentials",
				// ClientId, ClientSecret, TokenServiceURL intentionally empty
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	resolver, err := cf.NewDestinationServiceClient(context.Background(), map[string]any{
		"clientid": "p", "clientsecret": "s", "url": server.URL, "uri": server.URL,
	})
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}

	database := newManagerTestDB(t)
	tenant := db.CpiTenant{Name: "t", PirApiDestinationName: "no-creds-dest", CfApiEndpoint: "x", CfOrg: "o"}
	database.Create(&tenant)

	m := NewManager(database, resolver)
	_, err = m.Get(context.Background(), tenant.ID)
	if err == nil {
		t.Fatal("expected error for missing OAuth credentials, got nil")
	}
	if !strings.Contains(err.Error(), "missing OAuth credentials") {
		t.Errorf("error = %q; want to contain %q", err.Error(), "missing OAuth credentials")
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
