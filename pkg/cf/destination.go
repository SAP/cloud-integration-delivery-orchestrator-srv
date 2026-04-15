package cf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// DestinationServiceClient calls the BTP Destination Service REST API on behalf
// of a subscriber subaccount.
//
// Bootstrap path for destination management:
//  1. Obtain CF token (operator provides at apply/retry time).
//  2. Find the subscriber subaccount's Destination Service instance via CF API.
//  3. Create a temporary service key for that instance to get client credentials.
//  4. Use those credentials (client_credentials OAuth) to call the Destination
//     Service API — list, create, update destinations.
//  5. After bootstrap, delete the temporary service key.
//
// This client is constructed with the credentials from step 4 and is scoped to
// one bootstrap job.  It is never persisted.
type DestinationServiceClient struct {
	httpClient *http.Client
	apiURL     string      // e.g. "https://destination.cfapps.eu10.hana.ondemand.com"
	token      string      // Bearer token obtained via client_credentials flow
	tokenExp   time.Time
	clientID   string
	clientSecret string
	authURL    string      // OAuth token endpoint
}

// Destination is a BTP destination configuration entry.
//
// Fields map directly to the flat JSON format returned and accepted by the
// Destination Service REST API (/destination-configuration/v1/subaccountDestinations).
// OAuth2ClientCredentials fields (ClientId, ClientSecret, TokenServiceURL,
// TokenServiceURLType) and BasicAuthentication fields (User, Password) are
// top-level, not nested — this matches the actual API wire format.
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
}

// NewDestinationServiceClient constructs a DestinationServiceClient from credentials
// extracted from a Destination Service instance service key.
//
// The credentials map is the raw JSON from
// GET /v3/service_credential_bindings/{guid}/details → .credentials
func NewDestinationServiceClient(ctx context.Context, credentials map[string]any) (*DestinationServiceClient, error) {
	// Extract standard fields from Destination Service service key credentials.
	clientID, _ := credentials["clientid"].(string)
	clientSecret, _ := credentials["clientsecret"].(string)
	tokenURL, _ := credentials["url"].(string)
	apiURL, _ := credentials["uri"].(string)

	if clientID == "" || clientSecret == "" || tokenURL == "" || apiURL == "" {
		return nil, fmt.Errorf("cf/dest: incomplete Destination Service credentials (missing clientid/clientsecret/url/uri)")
	}
	// Normalise token URL.
	if len(tokenURL) > 0 && tokenURL[len(tokenURL)-1] != '/' {
		tokenURL += "/"
	}
	tokenURL += "oauth/token"

	c := &DestinationServiceClient{
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		apiURL:       apiURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		authURL:      tokenURL,
	}
	if err := c.refreshToken(ctx); err != nil {
		return nil, err
	}
	return c, nil
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

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return fmt.Errorf("cf/dest: decode token response: %w", err)
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

// ListDestinations returns all subaccount-level destinations in the subscriber
// subaccount's Destination Service instance.
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
//
// API: GET /destination-configuration/v1/subaccountDestinations/{name}
func (c *DestinationServiceClient) GetDestination(ctx context.Context, name string) (*Destination, error) {
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
	return &dest, nil
}

// UpsertDestination creates a new destination or fully replaces an existing one.
// It first checks whether the destination already exists; if so it uses PUT
// (update), otherwise POST (create).
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
	return nil
}
