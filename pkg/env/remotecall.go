package env

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/otel"
)

// HttpResponseError represents a non-2xx HTTP response from an external API.
type HttpResponseError struct {
	StatusCode int
	Body       []byte
	URL        string
}

func (e *HttpResponseError) Error() string {
	preview := string(e.Body)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return fmt.Sprintf("HTTP %d from %s: %s", e.StatusCode, e.URL, preview)
}

type HttpClient struct {
	HttpClient   *http.Client
	AccessToken  string
	ApiURL       string
	ClientId     string
	ClientSecret string
	AuthUrl      string
	TokenExp     time.Time  // proactive expiry; set by fetchToken from expires_in
	mu           sync.Mutex // protects AccessToken and TokenExp
}
type OauthResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Jti         string `json:"jti"`
}

type HttpRequest struct {
	ApiURL      string
	Method      string
	RequestBody []byte // raw body bytes; safe to reuse across retries
}

func NewClient(ctx context.Context, clientID string, clientSecret string, authUrl string, apiUrl string) (*HttpClient, error) {
	if !strings.HasSuffix(authUrl, "/oauth/token") {
		authUrl = fmt.Sprintf("%s/oauth/token", authUrl)
	}
	client := &HttpClient{
		HttpClient:   &http.Client{Transport: otel.WrapTransport(nil)},
		ApiURL:       apiUrl,
		ClientId:     clientID,
		ClientSecret: clientSecret,
		AuthUrl:      authUrl,
	}
	return client, nil
}

// fetchToken fetches a new OAuth token and stores it on the client (mutex-protected).
func (c *HttpClient) fetchToken(ctx context.Context) error {
	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", c.ClientId, c.ClientSecret))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.AuthUrl, payload)
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	res, errReq := c.HttpClient.Do(req)
	if errReq != nil {
		L(ctx).Errorw("error getting token response", "error", errReq)
		return errReq
	}
	defer res.Body.Close()
	body, errIOReader := io.ReadAll(res.Body)
	if errIOReader != nil {
		L(ctx).Errorw("error reading body from response", "error", errIOReader)
		return errIOReader
	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		L(ctx).Errorw("error extracting json data from response", "error", jsonUnmarshalErr)
		return jsonUnmarshalErr
	}

	c.mu.Lock()
	c.AccessToken = oauthResp.AccessToken
	c.TokenExp = time.Now().Add(time.Duration(oauthResp.ExpiresIn-30) * time.Second)
	c.mu.Unlock()
	return nil
}

// Do executes an HTTP request with the given context and returns the response body.
// Before each request it proactively refreshes the token if it is expired or within 30 s of expiry.
// On 401 (e.g. clock skew), it refreshes the token once and retries.
// On 429 (rate limit), it waits and retries up to 2 times with exponential backoff.
// Non-2xx responses are returned as *HttpResponseError.
// A single structured log entry is emitted per logical operation (including retries).
func (c *HttpClient) Do(ctx context.Context, request *HttpRequest) ([]byte, error) {
	start := time.Now()
	attempts := 1

	c.mu.Lock()
	needsToken := c.AccessToken == "" || time.Now().After(c.TokenExp)
	c.mu.Unlock()
	if needsToken {
		if err := c.fetchToken(ctx); err != nil {
			return nil, fmt.Errorf("proactive token refresh: %w", err)
		}
	}

	body, statusCode, err := c.doRequest(ctx, request)
	if err != nil {
		L(ctx).Errorw("http call",
			"method", request.Method, "url", request.ApiURL,
			"error", err.Error(), "attempts", attempts,
			"duration_ms", time.Since(start).Milliseconds())
		return nil, err
	}

	// 401: refresh token and retry once
	if statusCode == 401 {
		if err := c.fetchToken(ctx); err != nil {
			L(ctx).Errorw("http call",
				"method", request.Method, "url", request.ApiURL,
				"status", 401, "error", "token refresh failed: "+err.Error(),
				"attempts", attempts, "duration_ms", time.Since(start).Milliseconds())
			return nil, err
		}
		attempts++
		body, statusCode, err = c.doRequest(ctx, request)
		if err != nil {
			L(ctx).Errorw("http call",
				"method", request.Method, "url", request.ApiURL,
				"error", err.Error(), "attempts", attempts,
				"duration_ms", time.Since(start).Milliseconds())
			return nil, err
		}
	}

	// 429: retry with backoff (max 2 retries, 1s then 2s)
	for attempt := 0; statusCode == 429 && attempt < 2; attempt++ {
		wait := time.Duration(attempt+1) * time.Second
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		attempts++
		body, statusCode, err = c.doRequest(ctx, request)
		if err != nil {
			L(ctx).Errorw("http call",
				"method", request.Method, "url", request.ApiURL,
				"error", err.Error(), "attempts", attempts,
				"duration_ms", time.Since(start).Milliseconds())
			return nil, err
		}
	}

	// Emit one structured log per logical operation
	logFn := L(ctx).Infow
	if statusCode >= 500 {
		logFn = L(ctx).Errorw
	} else if statusCode >= 400 {
		logFn = L(ctx).Warnw
	}
	logFn("http call",
		"method", request.Method, "url", request.ApiURL,
		"status", statusCode, "attempts", attempts,
		"duration_ms", time.Since(start).Milliseconds(),
		"resp_size", len(body))

	// Non-2xx → error
	if statusCode < 200 || statusCode >= 300 {
		return nil, &HttpResponseError{StatusCode: statusCode, Body: body, URL: request.ApiURL}
	}

	return body, nil
}

func (c *HttpClient) doRequest(ctx context.Context, request *HttpRequest) ([]byte, int, error) {
	var req *http.Request
	if len(request.RequestBody) == 0 {
		req, _ = http.NewRequestWithContext(ctx, request.Method, request.ApiURL, nil)
	} else {
		req, _ = http.NewRequestWithContext(ctx, request.Method, request.ApiURL, bytes.NewReader(request.RequestBody))
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}

	c.mu.Lock()
	token := fmt.Sprintf("Bearer %s", c.AccessToken)
	c.mu.Unlock()

	req.Header.Add("Authorization", token)
	req.Header.Add("Accept", "application/json")
	resp, errReq := c.HttpClient.Do(req)

	if errReq != nil {
		return nil, 0, errReq
	}
	defer resp.Body.Close()

	respBody, errIOreader := io.ReadAll(resp.Body)
	if errIOreader != nil {
		return nil, 0, errIOreader
	}

	return respBody, resp.StatusCode, nil
}
