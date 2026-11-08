package cf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
)

// newCachingClient builds a DestinationServiceClient with the TTL cache enabled
// (like the provider-side VCAP client) but pointed at a test server, so the
// anti-stale write-back logic (§5.3) can be exercised directly.
func newCachingClient(t *testing.T, serverURL string) *DestinationServiceClient {
	t.Helper()
	httpClient, err := env.NewClient(context.Background(), "cid", "sec", serverURL, serverURL)
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	return &DestinationServiceClient{
		HttpClient: httpClient,
		ttl:        defaultDestTTL,
		cache:      make(map[string]*cachedDest),
		gen:        make(map[string]uint64),
	}
}

// If Invalidate runs while GetDestination is fetching off-lock, the fetched
// snapshot must NOT be written back to the cache (a later Get re-fetches).
func TestGetDestination_InvalidateDuringFetch_NoStaleWriteBack(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var served int32

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if strings.Contains(r.URL.Path, "/subaccountDestinations/") {
			// First destination GET blocks so the test can interleave Invalidate.
			if served == 0 {
				served = 1
				close(entered)
				<-release
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Destination{Name: "d", URL: server.URL, Authentication: "OAuth2ClientCredentials"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	c := newCachingClient(t, server.URL)

	done := make(chan struct{})
	go func() {
		_, _ = c.GetDestination(context.Background(), "d")
		close(done)
	}()

	<-entered           // GetDestination is parked mid-fetch (version captured)
	c.Invalidate("d")   // bump gen while the fetch is in flight
	close(release)      // let the fetch finish and attempt write-back
	<-done

	// The in-flight fetch must not have populated the cache.
	c.mu.RLock()
	_, cached := c.cache["d"]
	c.mu.RUnlock()
	if cached {
		t.Fatal("stale snapshot was written back to cache after Invalidate")
	}
}

// A normal (unraced) GetDestination does populate the cache, and a second Get
// is served from it without a second round-trip.
func TestGetDestination_CachesWhenUnraced(t *testing.T) {
	var hits int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		hits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Destination{Name: "d", URL: server.URL})
	}))
	defer server.Close()

	c := newCachingClient(t, server.URL)
	if _, err := c.GetDestination(context.Background(), "d"); err != nil {
		t.Fatalf("first get: %v", err)
	}
	if _, err := c.GetDestination(context.Background(), "d"); err != nil {
		t.Fatalf("second get: %v", err)
	}
	if hits != 1 {
		t.Fatalf("destination fetched %d times, want 1 (second served from cache)", hits)
	}
}
