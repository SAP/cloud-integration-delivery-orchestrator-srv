package cf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloudfoundry-community/go-cfenv"
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
	httpClient   *http.Client
	apiURL       string // e.g. "https://destination.cfapps.eu10.hana.ondemand.com"
	token        string // Bearer token obtained via client_credentials flow
	tokenExp     time.Time
	clientID     string
	clientSecret string
	authURL      string // OAuth token endpoint

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

	c := &DestinationServiceClient{
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		apiURL:       apiURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		authURL:      tokenURL,
		// ttl == 0: no cache for ephemeral subscriber clients
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	return c, nil
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

	c := &DestinationServiceClient{
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		apiURL:       apiURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		authURL:      authURL,
		ttl:          defaultDestTTL,
		cache:        make(map[string]*cachedDest),
	}
	if err := c.refreshToken(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
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

func (c *DestinationServiceClient) refreshToken(ctx context.Context) error {
	vals := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authURL,
		bytes.NewBufferString(vals.Encode()))
	if err != nil {
		return fmt.Errorf("cf/dest: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cf/dest: fetch token: %w", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cf/dest: token endpoint returned HTTP %d: %s", resp.StatusCode, buf.String())
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(buf.Bytes(), &tok); err != nil {
		return fmt.Errorf("cf/dest: decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("cf/dest: token endpoint returned empty access_token")
	}
	c.token = tok.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(tok.ExpiresIn-30) * time.Second)
	return nil
}

func (c *DestinationServiceClient) bearerToken(ctx context.Context) (string, error) {
	if time.Now().After(c.tokenExp) {
		if err := c.refreshToken(ctx); err != nil {
			return "", err
		}
	}
	return "Bearer " + c.token, nil
}

func (c *DestinationServiceClient) doJSON(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	url := c.apiURL + path

	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("cf/dest: marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = &bytes.Buffer{}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("cf/dest: build request %s %s: %w", method, path, err)
	}
	tok, err := c.bearerToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cf/dest: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return buf.Bytes(), resp.StatusCode, nil
}

// ListDestinations returns all subaccount-level destinations.
//
// API: GET /destination-configuration/v1/subaccountDestinations
func (c *DestinationServiceClient) ListDestinations(ctx context.Context) ([]Destination, error) {
	data, status, err := c.doJSON(ctx, http.MethodGet,
		"/destination-configuration/v1/subaccountDestinations", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("cf/dest: list destinations returned HTTP %d: %s", status, string(data))
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

	data, status, err := c.doJSON(ctx, http.MethodGet,
		"/destination-configuration/v1/subaccountDestinations/"+name, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("cf/dest: get destination %q returned HTTP %d: %s", name, status, string(data))
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

	data, status, err := c.doJSON(ctx, method,
		"/destination-configuration/v1/subaccountDestinations", dest)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("cf/dest: upsert destination %q returned HTTP %d: %s", dest.Name, status, string(data))
	}
	c.Invalidate(dest.Name)
	return nil
}
