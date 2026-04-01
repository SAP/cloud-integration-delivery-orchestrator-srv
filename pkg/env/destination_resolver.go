package env

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultDestTTL = 5 * time.Minute

// DestinationResolver provides on-demand, cached access to BTP Destination Service.
// It replaces the static destinationMap that was loaded once at startup.
type DestinationResolver struct {
	client *HttpClient // OAuth2 client for Destination Service API
	apiURL string      // Destination Service base URL

	ttl time.Duration

	mu    sync.RWMutex
	cache map[string]*cachedDest

	listMu    sync.RWMutex
	listCache *cachedList
}

type cachedDest struct {
	dest      Destination
	fetchedAt time.Time
}

type cachedList struct {
	dests     []Destination
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
	service := services[0]
	authUrl, _ := service.CredentialString("url")
	if !strings.HasSuffix(authUrl, "/oauth/token") {
		authUrl = fmt.Sprintf("%s/oauth/token", authUrl)
	}
	apiUrl, _ := service.CredentialString("uri")
	clientId, _ := service.CredentialString("clientid")
	clientSecret, _ := service.CredentialString("clientsecret")

	client, err := NewClient(context.Background(), clientId, clientSecret, authUrl, apiUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination service client: %w", err)
	}

	return &DestinationResolver{
		client: client,
		apiURL: apiUrl,
		ttl:    defaultDestTTL,
		cache:  make(map[string]*cachedDest),
	}, nil
}

// Get resolves a single destination by name with TTL caching.
func (r *DestinationResolver) Get(ctx context.Context, name string) (*Destination, error) {
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
func (r *DestinationResolver) List(ctx context.Context) ([]Destination, error) {
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

// fetchOne fetches a single destination by name from the Destination Service API.
func (r *DestinationResolver) fetchOne(ctx context.Context, name string) (*Destination, error) {
	url := fmt.Sprintf("%s/destination-configuration/v1/destinations/%s", r.apiURL, name)
	req := &HttpRequest{
		ApiURL: url,
		Method: http.MethodGet,
	}
	resp, statusCode, err := r.client.Do(ctx, req)
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
		DestinationConfiguration Destination `json:"destinationConfiguration"`
	}
	if err := json.Unmarshal(*resp, &wrapper); err != nil {
		// Fallback: try parsing directly as Destination (for subaccountDestinations compat)
		var dest Destination
		if err2 := json.Unmarshal(*resp, &dest); err2 != nil {
			return nil, fmt.Errorf("failed to unmarshal destination '%s': %w", name, err)
		}
		return &dest, nil
	}
	if wrapper.DestinationConfiguration.Name == "" {
		// The wrapper didn't match; parse directly
		var dest Destination
		if err := json.Unmarshal(*resp, &dest); err != nil {
			return nil, fmt.Errorf("failed to unmarshal destination '%s': %w", name, err)
		}
		return &dest, nil
	}
	return &wrapper.DestinationConfiguration, nil
}

// fetchAll fetches all subaccount destinations from the Destination Service API.
func (r *DestinationResolver) fetchAll(ctx context.Context) ([]Destination, error) {
	url := fmt.Sprintf("%s/destination-configuration/v1/subaccountDestinations", r.apiURL)
	req := &HttpRequest{
		ApiURL: url,
		Method: http.MethodGet,
	}
	resp, _, err := r.client.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subaccount destinations: %w", err)
	}
	var destinations []Destination
	if err := json.Unmarshal(*resp, &destinations); err != nil {
		return nil, fmt.Errorf("failed to unmarshal destinations: %w", err)
	}
	return destinations, nil
}
