package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mmt-delivery/pkg/cf"
)

func TestNewEmailClient_DestinationNotFound(t *testing.T) {
	// Mock destination service that returns 404 (destination not found)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "test-token",
				"token_type":   "bearer",
				"expires_in":   3600,
			})
			return
		}
		// Destination not found
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	resolver, err := cf.NewDestinationServiceClient(context.Background(), map[string]any{
		"uri":          server.URL,
		"clientid":     "test-client",
		"clientsecret": "test-secret",
		"url":          server.URL,
	})
	if err != nil {
		t.Fatalf("failed to create resolver: %v", err)
	}

	client, err := NewEmailClient(resolver, "NON_EXISTENT_DEST")
	if err == nil {
		t.Fatal("expected error when destination not found, got nil")
	}
	if client != nil {
		t.Fatal("expected nil client when destination not found")
	}
	expected := "mail service destination 'NON_EXISTENT_DEST' not found"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestNewEmailClient_EmptyDestName(t *testing.T) {
	client, err := NewEmailClient(nil, "")
	if err == nil {
		t.Fatal("expected error when destination name is empty, got nil")
	}
	if client != nil {
		t.Fatal("expected nil client when destination name is empty")
	}
	expected := "mail service destination name is empty"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}
