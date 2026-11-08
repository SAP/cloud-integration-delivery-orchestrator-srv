package tms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// tmsBackend is an httptest server that plays all three roles the Manager needs:
// the Destination Service (GET subaccountDestinations/{name}), the OAuth token
// endpoint, and the TMS API (GET /v2/nodes for candidate validation).
//
// getNodesStatus lets a test make validation fail.
type tmsBackend struct {
	server         *httptest.Server
	getNodesStatus int32 // atomic; 200 by default
}

func newTmsBackend(t *testing.T) *tmsBackend {
	t.Helper()
	b := &tmsBackend{getNodesStatus: http.StatusOK}
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/oauth/token"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/subaccountDestinations/"):
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(cf.Destination{
				Name: name, URL: b.server.URL, Authentication: "OAuth2ClientCredentials",
				TokenServiceURL: b.server.URL + "/oauth/token", ClientId: "cid", ClientSecret: "sec",
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v2/nodes"):
			if s := atomic.LoadInt32(&b.getNodesStatus); s != http.StatusOK {
				w.WriteHeader(int(s))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"nodes": []map[string]any{{"id": 1, "name": "N1"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(b.server.Close)
	return b
}

func (b *tmsBackend) resolver(t *testing.T) *cf.DestinationServiceClient {
	t.Helper()
	r, err := cf.NewDestinationServiceClient(context.Background(), map[string]any{
		"clientid": "p", "clientsecret": "s", "url": b.server.URL, "uri": b.server.URL,
	})
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	return r
}

func newTmsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=private"
	d, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := d.AutoMigrate(&db.CentralTmsContext{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

func okCommit(cf.Destination, time.Time) error { return nil }

// A successful Reconfigure installs the validated candidate as the active
// client; a second one replaces it with a fresh instance. (The config version
// is owned by the DB row, exercised at the service layer — not here.)
func TestReconfigure_InstallsValidatedCandidate(t *testing.T) {
	b := newTmsBackend(t)
	m := NewManager(newTmsTestDB(t), b.resolver(t))

	if _, err := m.Reconfigure(context.Background(), "dest-a", okCommit); err != nil {
		t.Fatalf("first reconfigure: %v", err)
	}
	c1, err := m.Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := m.Reconfigure(context.Background(), "dest-b", okCommit); err != nil {
		t.Fatalf("second reconfigure: %v", err)
	}
	c2, _ := m.Get(context.Background())
	if c1 == c2 {
		t.Fatal("expected a new active client after the second reconfigure")
	}
}

// Validation failure (GetNodes non-200) returns an error, does not run commit,
// and does not replace the active client.
func TestReconfigure_ValidationFailurePreservesState(t *testing.T) {
	b := newTmsBackend(t)
	m := NewManager(newTmsTestDB(t), b.resolver(t))

	// Seed a good active entry first.
	if _, err := m.Reconfigure(context.Background(), "good", okCommit); err != nil {
		t.Fatalf("seed reconfigure: %v", err)
	}
	active, _ := m.Get(context.Background())

	// Now make GetNodes fail and attempt another reconfigure.
	atomic.StoreInt32(&b.getNodesStatus, http.StatusInternalServerError)
	committed := false
	_, err := m.Reconfigure(context.Background(), "bad", func(cf.Destination, time.Time) error {
		committed = true
		return nil
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if committed {
		t.Fatal("commit must not run when validation fails")
	}
	after, _ := m.Get(context.Background())
	if after != active {
		t.Fatal("active client must be preserved after failed reconfigure")
	}
}

// Commit failure (e.g. DB transaction error, version conflict) returns the
// commit error and does not replace the active client.
func TestReconfigure_CommitFailurePreservesState(t *testing.T) {
	b := newTmsBackend(t)
	m := NewManager(newTmsTestDB(t), b.resolver(t))

	if _, err := m.Reconfigure(context.Background(), "good", okCommit); err != nil {
		t.Fatalf("seed reconfigure: %v", err)
	}
	active, _ := m.Get(context.Background())

	wantErr := "db boom"
	_, err := m.Reconfigure(context.Background(), "next", func(cf.Destination, time.Time) error {
		return errTest(wantErr)
	})
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("expected commit error %q, got %v", wantErr, err)
	}
	after, _ := m.Get(context.Background())
	if after != active {
		t.Fatal("active client must be preserved after commit failure")
	}
}

// Cold-start lazy build (no prior Reconfigure) builds a usable client from the
// saved CentralTmsContext, without running any commit or Rule 4.
func TestEnsureLoaded_ColdStartBuildsClient(t *testing.T) {
	b := newTmsBackend(t)
	d := newTmsTestDB(t)
	if err := d.Create(&db.CentralTmsContext{TmsApiDestinationName: "seeded"}).Error; err != nil {
		t.Fatalf("seed ctx: %v", err)
	}
	m := NewManager(d, b.resolver(t))

	c, err := m.Get(context.Background())
	if err != nil {
		t.Fatalf("get (lazy build): %v", err)
	}
	if c == nil {
		t.Fatal("expected a client after lazy build")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
