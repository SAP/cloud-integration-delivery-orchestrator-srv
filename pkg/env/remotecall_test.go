package env

import (
	"context"
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
