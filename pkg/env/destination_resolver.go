package env

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"mmt-delivery/pkg/cf"
)

const defaultDestTTL = 5 * time.Minute

// DestinationResolver provides on-demand, cached access to the provider-side
// BTP Destination Service (bound via VCAP_SERVICES).
//
// Read path  — Get / List: calls the resolution endpoint
//   (/destination-configuration/v1/destinations/{name}) which returns merged
//   instance+subaccount configuration including injected token information.
//   Results are cached with a TTL so repeated lookups within one request
//   do not fan out to the Destination Service.
//
// Write path — Upsert: delegates to cf.DestinationServiceClient.UpsertDestination
//   (the management endpoint, POST/PUT /subaccountDestinations).  The same HTTP
//   implementation is reused; no duplicate code here.
//
// Both paths use cf.Destination as the canonical type, which maps the flat
// JSON wire format of the Destination Service API directly.
type DestinationResolver struct {
	readClient  *HttpClient            // OAuth2 client for the resolution (read) endpoint
	writeClient *cf.DestinationServiceClient // management client for POST/PUT operations
	apiURL      string                 // Destination Service base URL

	ttl time.Duration

	mu    sync.RWMutex
	cache map[string]*cachedDest

	listMu    sync.RWMutex
	listCache *cachedList
}

type cachedDest struct {
	dest      cf.Destination
	fetchedAt time.Time
}

type cachedList struct {
	dests     []cf.Destination
	fetchedAt time.Time
}

// NewDestinationResolver creates a resolver using the Destination Service binding from VCAP_SERVICES.
func NewDestinationResolver() (*DestinationResolver, error) {
	if appEnv == nil {
		return nil, fmt.Errorf("env.Init() must be called before NewDestinationResolver()")
	}

	services, err := appEnv.Services.WithLabel("destination")
	if err != nil || len(services) == 0 {
		return nil, fmt.Errorf("failed to get service with label 'destination'")
	}
	svc := services[0]
	authURL, _ := svc.CredentialString("url")
	if !strings.HasSuffix(authURL, "/oauth/token") {
		authURL = fmt.Sprintf("%s/oauth/token", authURL)
	}
	apiURL, _ := svc.CredentialString("uri")
	clientID, _ := svc.CredentialString("clientid")
	clientSecret, _ := svc.CredentialString("clientsecret")

	// Read client: uses the internal HttpClient (supports 401 token refresh via
	// the existing pkg/env OAuth2 flow).
	readClient, err := NewClient(context.Background(), clientID, clientSecret, authURL, apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination read client: %w", err)
	}

	// Write client: reuses cf.DestinationServiceClient so POST/PUT logic
	// is not duplicated here.  Same credentials, same Destination Service instance.
	creds := map[string]any{
		"clientid":     clientID,
		"clientsecret": clientSecret,
		"url":          authURL,
		"uri":          apiURL,
	}
	writeClient, err := cf.NewDestinationServiceClient(context.Background(), creds)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination write client: %w", err)
	}

	return &DestinationResolver{
		readClient:  readClient,
		writeClient: writeClient,
		apiURL:      apiURL,
		ttl:         defaultDestTTL,
		cache:       make(map[string]*cachedDest),
	}, nil
}

// Get resolves a single destination by name with TTL caching.
//
// Uses the resolution endpoint (/v1/destinations/{name}) which returns merged
// configuration with injected token information — suitable for runtime callers
// (TrResolver, notify) that need live credentials.
func (r *DestinationResolver) Get(ctx context.Context, name string) (*cf.Destination, error) {
	// Fast path: read lock
	r.mu.RLock()
	if entry, ok := r.cache[name]; ok && time.Since(entry.fetchedAt) < r.ttl {
		r.mu.RUnlock()
		return &entry.dest, nil
	}
	r.mu.RUnlock()

	// Slow path: fetch from Destination Service
	dest, err := r.fetchOne(ctx, name)
	if err != nil {
		return nil, err
	}

	// Write to cache
	r.mu.Lock()
	r.cache[name] = &cachedDest{dest: *dest, fetchedAt: time.Now()}
	r.mu.Unlock()

	return dest, nil
}

// List returns all subaccount destinations with TTL caching.
func (r *DestinationResolver) List(ctx context.Context) ([]cf.Destination, error) {
	// Fast path
	r.listMu.RLock()
	if r.listCache != nil && time.Since(r.listCache.fetchedAt) < r.ttl {
		dests := r.listCache.dests
		r.listMu.RUnlock()
		return dests, nil
	}
	r.listMu.RUnlock()

	// Slow path: fetch all
	dests, err := r.fetchAll(ctx)
	if err != nil {
		return nil, err
	}

	// Write to cache
	r.listMu.Lock()
	r.listCache = &cachedList{dests: dests, fetchedAt: time.Now()}
	r.listMu.Unlock()

	// Also populate individual cache entries
	r.mu.Lock()
	now := time.Now()
	for _, d := range dests {
		r.cache[d.Name] = &cachedDest{dest: d, fetchedAt: now}
	}
	r.mu.Unlock()

	return dests, nil
}

// Upsert writes a destination to the provider-side Destination Service.
// Uses PUT if the destination already exists (by name), POST if it does not.
// Invalidates the in-memory cache entry for dest.Name on success.
//
// Delegates to cf.DestinationServiceClient.UpsertDestination — no duplicate
// HTTP implementation here.  Called during tenant bootstrap to create the
// per-tenant CPIDELIVERY_PIR_{id} and CPIDELIVERY_CAS_{id} destinations.
func (r *DestinationResolver) Upsert(ctx context.Context, dest cf.Destination) error {
	if err := r.writeClient.UpsertDestination(ctx, dest); err != nil {
		return fmt.Errorf("destination resolver: upsert %q: %w", dest.Name, err)
	}
	r.Invalidate(dest.Name)
	return nil
}

// Invalidate removes a specific destination from the cache, forcing a fresh fetch on next Get.
func (r *DestinationResolver) Invalidate(name string) {
	r.mu.Lock()
	delete(r.cache, name)
	r.mu.Unlock()
}

// InvalidateAll clears the entire cache.
func (r *DestinationResolver) InvalidateAll() {
	r.mu.Lock()
	r.cache = make(map[string]*cachedDest)
	r.mu.Unlock()

	r.listMu.Lock()
	r.listCache = nil
	r.listMu.Unlock()
}

// fetchOne fetches a single destination by name from the resolution endpoint.
//
// Note: uses /v1/destinations/{name} (not /v1/subaccountDestinations/{name}).
// The resolution endpoint merges instance- and subaccount-level configuration
// and injects live token information, which is what runtime callers need.
func (r *DestinationResolver) fetchOne(ctx context.Context, name string) (*cf.Destination, error) {
	url := fmt.Sprintf("%s/destination-configuration/v1/destinations/%s", r.apiURL, name)
	req := &HttpRequest{
		ApiURL: url,
		Method: http.MethodGet,
	}
	resp, statusCode, err := r.readClient.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch destination '%s': %w", name, err)
	}
	if statusCode == 404 {
		return nil, fmt.Errorf("destination '%s' not found", name)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("destination service returned status %d for '%s'", statusCode, name)
	}

	// The /destinations/{name} endpoint returns {"owner": {...}, "destinationConfiguration": {...}}
	// Parse the destinationConfiguration field.
	var wrapper struct {
		DestinationConfiguration cf.Destination `json:"destinationConfiguration"`
	}
	if err := json.Unmarshal(*resp, &wrapper); err != nil || wrapper.DestinationConfiguration.Name == "" {
		// Fallback: response may already be a flat Destination object.
		var dest cf.Destination
		if err2 := json.Unmarshal(*resp, &dest); err2 != nil {
			return nil, fmt.Errorf("failed to unmarshal destination '%s': %w", name, err)
		}
		return &dest, nil
	}
	return &wrapper.DestinationConfiguration, nil
}

// fetchAll fetches all subaccount destinations from the management endpoint.
func (r *DestinationResolver) fetchAll(ctx context.Context) ([]cf.Destination, error) {
	url := fmt.Sprintf("%s/destination-configuration/v1/subaccountDestinations", r.apiURL)
	req := &HttpRequest{
		ApiURL: url,
		Method: http.MethodGet,
	}
	resp, _, err := r.readClient.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subaccount destinations: %w", err)
	}
	var destinations []cf.Destination
	if err := json.Unmarshal(*resp, &destinations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal destinations: %w", err)
	}
	return destinations, nil
}
