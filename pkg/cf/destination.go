package cf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudfoundry-community/go-cfenv"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/consts"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
)

const defaultDestTTL = 5 * time.Minute

// DestinationServiceClient is the single entry point for all BTP Destination
// Service operations — reads and writes, provider-side and subscriber-side.
//
// Provider-side (long-lived singleton, TTL cache enabled):
//
//	NewDestinationServiceClientFromVCAP reads credentials from VCAP_SERVICES
//	(the "destination" service binding).  Used by runtime callers (TrResolver,
//	notify, handler) to look up CPIDELIVERY_PIR_{id}, CPIDELIVERY_CAS_{id},
//	SMTP, JIRA, and github destinations.
//
// Subscriber-side (ephemeral, no cache):
//
//	NewDestinationServiceClient accepts a raw credentials map obtained from a
//	temporary CF service key.  Used during tenant bootstrap to read/write
//	CloudIntegration / ContentAssemblyService destinations in the subscriber's
//	own Destination Service instance.  The client is discarded after the job.
//
// Destination Service endpoints used:
//
//	GET  /destination-configuration/v1/subaccountDestinations       (list)
//	GET  /destination-configuration/v1/subaccountDestinations/{name} (get)
//	POST /destination-configuration/v1/subaccountDestinations       (create)
//	PUT  /destination-configuration/v1/subaccountDestinations       (update)
type DestinationServiceClient struct {
	// Embedded shared OAuth infra (pkg/env): proactive token refresh plus
	// 401-reactive refresh-and-retry, mutex-protected token, otel transport.
	// This is the same client used by pkg/cpi, pkg/cas, pkg/tms and pkg/xsuaa.
	*env.HttpClient

	// TTL cache — optional.  Zero ttl disables caching.
	ttl   time.Duration
	mu    sync.RWMutex
	cache map[string]*cachedDest
}

type cachedDest struct {
	dest      Destination
	fetchedAt time.Time
}

// Destination is a BTP destination configuration entry.
//
// Fields map directly to the flat JSON format returned and accepted by the
// Destination Service REST API (/destination-configuration/v1/subaccountDestinations).
// OAuth2ClientCredentials fields (ClientId, ClientSecret, TokenServiceURL,
// TokenServiceURLType) and BasicAuthentication fields (User, Password) are
// top-level, not nested — this matches the actual API wire format.
//
// AdditionalProperties holds destination-type-specific fields that don't have a
// fixed slot in this struct (e.g. "sourceSystemId" for TMS destinations).
// On marshal they are merged into the top-level JSON object alongside the fixed fields.
type Destination struct {
	Name                string `json:"Name"`
	Description         string `json:"Description,omitempty"`
	Type                string `json:"Type"`
	URL                 string `json:"URL,omitempty"`
	Authentication      string `json:"Authentication"`
	ProxyType           string `json:"ProxyType,omitempty"`
	TokenServiceURL     string `json:"tokenServiceURL,omitempty"`
	TokenServiceURLType string `json:"tokenServiceURLType,omitempty"`
	ClientId            string `json:"clientId,omitempty"`
	ClientSecret        string `json:"clientSecret,omitempty"`
	User                string `json:"User,omitempty"`
	Password            string `json:"Password,omitempty"`
	Port                string `json:"Port,omitempty"`
	SourceSystemId      string `json:"sourceSystemId,omitempty"`
}

// NewDestinationServiceClient constructs a DestinationServiceClient from credentials
// extracted from a Destination Service instance service key.
//
// The credentials map is the raw JSON from
// GET /v3/service_credential_bindings/{guid}/details → .credentials
//
// Used for subscriber-side bootstrap operations (ephemeral, no TTL cache).
func NewDestinationServiceClient(ctx context.Context, credentials map[string]any) (*DestinationServiceClient, error) {
	clientID, _ := credentials["clientid"].(string)
	clientSecret, _ := credentials["clientsecret"].(string)
	tokenURL, _ := credentials["url"].(string)
	apiURL, _ := credentials["uri"].(string)

	if clientID == "" || clientSecret == "" || tokenURL == "" || apiURL == "" {
		return nil, fmt.Errorf("cf/dest: incomplete Destination Service credentials (missing clientid/clientsecret/url/uri)")
	}
	tokenURL = NormaliseTokenURL(tokenURL)

	httpClient, err := env.NewClient(ctx, clientID, clientSecret, tokenURL, apiURL)
	if err != nil {
		return nil, fmt.Errorf("cf/dest: create http client: %w", err)
	}
	return &DestinationServiceClient{
		HttpClient: httpClient,
		// ttl == 0: no cache for ephemeral subscriber clients
	}, nil
}

// NewDestinationServiceClientFromVCAP constructs a DestinationServiceClient from
// the "destination" service binding in VCAP_SERVICES.
//
// Used to create the long-lived provider-side client in main().  TTL cache is
// enabled (defaultDestTTL = 5 min) to reduce Destination Service round-trips
// during normal runtime operation.
func NewDestinationServiceClientFromVCAP(appEnv *cfenv.App) (*DestinationServiceClient, error) {
	if appEnv == nil {
		return nil, fmt.Errorf("cf/dest: appEnv is nil — env.Init() must be called first")
	}
	services, err := appEnv.Services.WithLabel("destination")
	if err != nil || len(services) == 0 {
		return nil, fmt.Errorf("cf/dest: no service with label 'destination' found in VCAP_SERVICES")
	}
	svc := services[0]
	authURL, _ := svc.CredentialString("url")
	apiURL, _ := svc.CredentialString("uri")
	clientID, _ := svc.CredentialString("clientid")
	clientSecret, _ := svc.CredentialString("clientsecret")

	if clientID == "" || clientSecret == "" || authURL == "" || apiURL == "" {
		return nil, fmt.Errorf("cf/dest: incomplete 'destination' service credentials in VCAP_SERVICES")
	}
	authURL = NormaliseTokenURL(authURL)

	httpClient, err := env.NewClient(context.Background(), clientID, clientSecret, authURL, apiURL)
	if err != nil {
		return nil, fmt.Errorf("cf/dest: create http client: %w", err)
	}
	return &DestinationServiceClient{
		HttpClient: httpClient,
		ttl:        defaultDestTTL,
		cache:      make(map[string]*cachedDest),
	}, nil
}

// NormaliseTokenURL ensures an OAuth token URL ends with /oauth/token.
// SAP BTP service key credentials often provide only the UAA base host
// (e.g. "https://tenant.authentication.eu10.hana.ondemand.com"); this
// function appends the path so the result is a valid token endpoint.
func NormaliseTokenURL(u string) string {
	if strings.HasSuffix(u, "/oauth/token") {
		return u
	}
	if !strings.HasSuffix(u, "/") {
		u += "/"
	}
	return u + "oauth/token"
}

// ListDestinations returns all subaccount-level destinations.
//
// Requests go through the embedded env.HttpClient, which owns OAuth token
// handling (proactive + 401-reactive refresh) and 429 backoff; non-2xx
// responses come back as *env.HttpResponseError. Per-request timeout is a
// context deadline, matching pkg/cpi and pkg/tms.
//
// API: GET /destination-configuration/v1/subaccountDestinations
func (c *DestinationServiceClient) ListDestinations(ctx context.Context) ([]Destination, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: c.ApiURL + "/destination-configuration/v1/subaccountDestinations",
	}
	data, err := c.Do(childCtx, &request)
	if err != nil {
		return nil, err
	}
	var dests []Destination
	if err := json.Unmarshal(data, &dests); err != nil {
		return nil, fmt.Errorf("cf/dest: decode destinations: %w", err)
	}
	return dests, nil
}

// GetDestination returns a single destination by name, or nil if not found.
// Results are TTL-cached when the client was created with a non-zero ttl
// (i.e. NewDestinationServiceClientFromVCAP).
//
// API: GET /destination-configuration/v1/subaccountDestinations/{name}
func (c *DestinationServiceClient) GetDestination(ctx context.Context, name string) (*Destination, error) {
	// Fast path: read cache
	if c.ttl > 0 {
		c.mu.RLock()
		if entry, ok := c.cache[name]; ok && time.Since(entry.fetchedAt) < c.ttl {
			dest := entry.dest
			c.mu.RUnlock()
			return &dest, nil
		}
		c.mu.RUnlock()
	}

	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: c.ApiURL + "/destination-configuration/v1/subaccountDestinations/" + name,
	}
	data, err := c.Do(childCtx, &request)
	if err != nil {
		// 404 → destination does not exist; map to (nil, nil) so callers
		// (notably UpsertDestination) can distinguish create vs update.
		var httpErr *env.HttpResponseError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	var dest Destination
	if err := json.Unmarshal(data, &dest); err != nil {
		return nil, fmt.Errorf("cf/dest: decode destination %q: %w", name, err)
	}

	// Populate cache
	if c.ttl > 0 {
		c.mu.Lock()
		c.cache[name] = &cachedDest{dest: dest, fetchedAt: time.Now()}
		c.mu.Unlock()
	}

	return &dest, nil
}

// Invalidate removes a specific destination from the cache.
func (c *DestinationServiceClient) Invalidate(name string) {
	if c.ttl == 0 {
		return
	}
	c.mu.Lock()
	delete(c.cache, name)
	c.mu.Unlock()
}

// InvalidateAll clears the entire destination cache.
func (c *DestinationServiceClient) InvalidateAll() {
	if c.ttl == 0 {
		return
	}
	c.mu.Lock()
	c.cache = make(map[string]*cachedDest)
	c.mu.Unlock()
}

// UpsertDestination creates a new destination or fully replaces an existing one.
// It first checks whether the destination already exists; if so it uses PUT
// (update), otherwise POST (create).
// Invalidates the cache entry for dest.Name on success.
//
// API: POST /destination-configuration/v1/subaccountDestinations  (create)
//
//	PUT  /destination-configuration/v1/subaccountDestinations  (update)
func (c *DestinationServiceClient) UpsertDestination(ctx context.Context, dest Destination) error {
	existing, err := c.GetDestination(ctx, dest.Name)
	if err != nil {
		return err
	}

	method := http.MethodPost
	if existing != nil {
		method = http.MethodPut
	}

	payload, err := json.Marshal(dest)
	if err != nil {
		return fmt.Errorf("cf/dest: marshal destination %q: %w", dest.Name, err)
	}
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	request := env.HttpRequest{
		Method:      method,
		ApiURL:      c.ApiURL + "/destination-configuration/v1/subaccountDestinations",
		RequestBody: payload,
	}
	if _, err := c.Do(childCtx, &request); err != nil {
		return err
	}
	c.Invalidate(dest.Name)
	return nil
}

// DeleteDestination removes a subaccount destination by name. A 404 is treated as
// success (idempotent delete): the caller only cares that the destination is gone.
// Invalidates the cache entry for name on success.
//
// API: DELETE /destination-configuration/v1/subaccountDestinations/{name}
func (c *DestinationServiceClient) DeleteDestination(ctx context.Context, name string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	// Evict on every exit path: cache invalidation is always safe (a later read just
	// re-fetches), so we needn't special-case success vs error.
	defer c.Invalidate(name)
	request := env.HttpRequest{
		Method: http.MethodDelete,
		ApiURL: c.ApiURL + "/destination-configuration/v1/subaccountDestinations/" + name,
	}
	if _, err := c.Do(childCtx, &request); err != nil {
		// 404 → destination already absent; treat as a successful (idempotent) delete.
		var httpErr *env.HttpResponseError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}
