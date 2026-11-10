package env

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_RetryPreservesBody(t *testing.T) {
	var attempt atomic.Int32
	expectedBody := `{"transportRequests":[601363,601364]}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		body := string(buf[:n])

		count := attempt.Add(1)
		if count == 1 {
			// First attempt: return 429
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		// Second attempt: verify body is still present
		if body != expectedBody {
			t.Errorf("retry body = %q, want %q", body, expectedBody)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"actionId":999}`))
	}))
	defer server.Close()

	client := &HttpClient{
		HttpClient:  server.Client(),
		AccessToken: "test-token",
		TokenExp:    time.Now().Add(1 * time.Hour),
		ApiURL:      server.URL,
	}

	request := &HttpRequest{
		Method:      http.MethodPost,
		ApiURL:      server.URL + "/v2/nodes/123/transportRequests/import",
		RequestBody: []byte(expectedBody),
	}

	body, err := client.Do(context.Background(), request)
	if err != nil {
		t.Fatalf("Do() returned error: %v", err)
	}
	if string(body) != `{"actionId":999}` {
		t.Fatalf("unexpected response: %s", body)
	}
	if attempt.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempt.Load())
	}
}

func TestDo_401RetryPreservesBody(t *testing.T) {
	var attempt atomic.Int32
	expectedBody := `{"data":"test"}`

	// Token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"new-token","token_type":"bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	// API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		body := string(buf[:n])

		count := attempt.Add(1)
		if count == 1 {
			// First attempt: return 401
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Second attempt: verify body and new token
		if body != expectedBody {
			t.Errorf("retry body = %q, want %q", body, expectedBody)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer new-token" {
			t.Errorf("expected new token, got %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := &HttpClient{
		HttpClient:   server.Client(),
		AccessToken:  "old-token",
		TokenExp:     time.Now().Add(1 * time.Hour),
		ApiURL:       server.URL,
		AuthUrl:      tokenServer.URL + "/oauth/token",
		ClientId:     "test",
		ClientSecret: "test",
	}

	request := &HttpRequest{
		Method:      http.MethodPost,
		ApiURL:      server.URL + "/api/test",
		RequestBody: []byte(expectedBody),
	}

	body, err := client.Do(context.Background(), request)
	if err != nil {
		t.Fatalf("Do() returned error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected response: %s", body)
	}
	if attempt.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempt.Load())
	}
}

// fetchToken (via Authenticate) must reject a non-2xx token response instead of
// treating it as a successful empty-token auth.
func TestAuthenticate_RejectsNon2xxTokenResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}))
	defer server.Close()

	c, err := NewClient(context.Background(), "cid", "sec", server.URL, server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	authErr := c.Authenticate(context.Background())
	if authErr == nil {
		t.Fatal("expected error for 404 token response, got nil")
	}
	if c.AccessToken != "" {
		t.Fatalf("expected no token stored after failed auth, got %q", c.AccessToken)
	}
	// R2: a token-endpoint 404 must NOT be mistakable for a business-API 404.
	var tokErr *TokenError
	if !errors.As(authErr, &tokErr) {
		t.Fatalf("expected *TokenError, got %T", authErr)
	}
	var httpErr *HttpResponseError
	if errors.As(authErr, &httpErr) {
		t.Fatal("token error must NOT match *HttpResponseError (would be read as object-not-found)")
	}
}

// A 2xx token response with an empty access_token is not a usable auth.
func TestAuthenticate_RejectsEmptyAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"","expires_in":3600}`))
	}))
	defer server.Close()

	c, err := NewClient(context.Background(), "cid", "sec", server.URL, server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Authenticate(context.Background()); err == nil {
		t.Fatal("expected error for empty access_token, got nil")
	}
}

// Authenticate succeeds on a valid token response and reuses the token (no
// second fetch) while it is still valid.
func TestAuthenticate_SucceedsAndReuses(t *testing.T) {
	var fetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer server.Close()

	c, err := NewClient(context.Background(), "cid", "sec", server.URL, server.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("first authenticate: %v", err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("second authenticate: %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("token fetched %d times, want 1 (second reused)", n)
	}
}
