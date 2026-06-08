package cas

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mmt-delivery/pkg/env"
)

func createTestClient(apiURL string) *CasClient {
	return &CasClient{
		HttpClient: &env.HttpClient{
			HttpClient:  &http.Client{},
			AccessToken: "test-token",
			ApiURL:      apiURL,
			TokenExp:    time.Now().Add(24 * time.Hour),
		},
	}
}

func TestListCloudIntegrationResources_FiltersRequestedPackages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET method, got %s", r.Method)
		}
		if r.URL.Path != "/v1/contentResources" {
			t.Fatalf("expected /v1/contentResources, got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("filters"); got != "type eq 'Cloud Integration'" {
			t.Fatalf("unexpected filters query: %q", got)
		}

		resp := map[string]any{
			"contentResources": []CatalogContentResource{
				{ID: "pkg-1", Name: "Package 1", SubType: "package"},
				{ID: "pkg-2", Name: "Package 2", SubType: "package"},
				{ID: "dest-1", Name: "Destination", SubType: "Destination"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)

	filtered, err := client.ListCloudIntegrationResources(context.Background(), []string{"pkg-2"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "pkg-2" {
		t.Fatalf("expected only pkg-2, got %+v", filtered)
	}

	all, err := client.ListCloudIntegrationResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected unfiltered call to succeed, got %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected all resources, got %d", len(all))
	}
}

func TestListCloudIntegrationResources_UnexpectedStatusIncludesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend exploded", http.StatusBadGateway)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	_, err := client.ListCloudIntegrationResources(context.Background(), nil)
	if err == nil {
		t.Fatal("expected unexpected status error, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected status 502") || !strings.Contains(err.Error(), "backend exploded") {
		t.Fatalf("expected status and body in error, got %v", err)
	}
}

func TestTriggerExport_SendsPayloadAndReturnsProcessID(t *testing.T) {
	var received ExportRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST method, got %s", r.Method)
		}
		if r.URL.Path != "/v1/contentResources/export" {
			t.Fatalf("expected export path, got %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"activityId":"act-1","processId":"proc-1","state":"INITIAL"}`))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	resp, err := client.TriggerExport(context.Background(), ExportRequest{
		ID:          "dr-1",
		Description: "test export",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.ProcessID != "proc-1" {
		t.Fatalf("process ID = %q, want proc-1", resp.ProcessID)
	}
	if received.ID != "dr-1" || received.Description != "test export" {
		t.Fatalf("unexpected request body: %+v", received)
	}
}

func TestTriggerExport_RejectsMissingProcessID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"activityId":"act-1","state":"INITIAL"}`))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	_, err := client.TriggerExport(context.Background(), ExportRequest{ID: "dr-2"})
	if err == nil {
		t.Fatal("expected missing processId error, got nil")
	}
	if !strings.Contains(err.Error(), "response missing processId") {
		t.Fatalf("expected missing processId error, got %v", err)
	}
}

func TestPollOperation_SuccessAndUnexpectedStatus(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/operations/proc-1" || r.URL.RawQuery != "messages=true" {
				t.Fatalf("unexpected poll request: %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"processId":"proc-1","state":"FINISHED","progress":100}`))
		}))
		defer server.Close()

		client := createTestClient(server.URL)
		status, err := client.PollOperation(context.Background(), "proc-1")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if status.State != "FINISHED" || status.Progress != 100 {
			t.Fatalf("unexpected poll status: %+v", status)
		}
	})

	t.Run("unexpected status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "still booting", http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := createTestClient(server.URL)
		_, err := client.PollOperation(context.Background(), "proc-2")
		if err == nil {
			t.Fatal("expected unexpected status error, got nil")
		}
		if !strings.Contains(err.Error(), "unexpected status 503") {
			t.Fatalf("expected 503 error, got %v", err)
		}
	})
}

func TestGetOperationConfig_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/operations/proc-1/config" || r.URL.RawQuery != "logs=true" {
			t.Fatalf("unexpected config request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"activityID":"act-1","transportRequestID":"TR-123","transportRequestURL":"https://example/tr/TR-123"}`))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	cfg, err := client.GetOperationConfig(context.Background(), "proc-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.TransportRequestID != "TR-123" {
		t.Fatalf("transportRequestID = %q, want TR-123", cfg.TransportRequestID)
	}
}

func TestGetActivities_SuccessWithTopFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/activities" {
			t.Fatalf("unexpected activities path: %s", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("filters") != "requestor eq 'CPIDelivery'" {
			t.Fatalf("unexpected requestor filter: %q", query.Get("filters"))
		}
		if query.Get("top") != "5" {
			t.Fatalf("unexpected top filter: %q", query.Get("top"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"activities":[{"activityId":"act-1","processId":"proc-1","state":"FINISHED"}]}`))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	activities, err := client.GetActivities(context.Background(), "CPIDelivery", 5)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(activities) != 1 || activities[0].ActivityID != "act-1" {
		t.Fatalf("unexpected activities: %+v", activities)
	}
}

func TestSafeBody_HandlesNilAndTruncatesLongBodies(t *testing.T) {
	if got := safeBody(nil); got != "<nil>" {
		t.Fatalf("safeBody(nil) = %q, want <nil>", got)
	}

	longBody := bytes.Repeat([]byte("a"), 301)
	got := safeBody(&longBody)
	if len(got) != 303 {
		t.Fatalf("expected truncated body length 303, got %d", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got[len(got)-3:])
	}
}
