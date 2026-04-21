package tms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mmt-delivery/pkg/env"
)

// testTransportNode is a local type for testing, mirrors db.TransportNode
type testTransportNode struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// testTMSNodesResp is used for testing GetNodes responses
type testTMSNodesResp struct {
	Nodes []testTransportNode `json:"nodes"`
}

// createTestClient creates a TmsClient for testing with the given API URL
func createTestClient(apiURL string) *TmsClient {
	return &TmsClient{
		HttpClient: &env.HttpClient{
			HttpClient:  &http.Client{},
			AccessToken: "test-token",
			ApiURL:      apiURL,
			TokenExp:    time.Now().Add(24 * time.Hour),
		},
	}
}

// =============================================================================
// ImportTransportRequest Tests
// =============================================================================

func TestImportTransportRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json;charset=UTF-8" {
			t.Errorf("Expected Content-Type application/json;charset=UTF-8, got %s", contentType)
		}

		resp := ReqImportTransportResp{ActionID: 12345, MonitoringURL: "https://example.com/monitor"}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	actionID, err := client.ImportTransportRequest(context.Background(), 1, []uint{100, 101})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if actionID != 12345 {
		t.Errorf("Expected actionID 12345, got %d", actionID)
	}
}

func TestImportTransportRequest_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the timeout
		time.Sleep(3 * time.Second)
		resp := ReqImportTransportResp{ActionID: 12345}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client with a short timeout context for testing
	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.ImportTransportRequest(shortCtx, 1, []uint{100})

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

func TestImportTransportRequest_InvalidActionID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ReqImportTransportResp{ActionID: 0}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	_, err := client.ImportTransportRequest(context.Background(), 1, []uint{100})

	if err == nil {
		t.Error("Expected error for zero actionId, got nil")
	}
	expectedErrMsg := "failed to trigger import"
	if err != nil && !containsSubstring(err.Error(), expectedErrMsg) {
		t.Errorf("Expected error containing '%s', got %v", expectedErrMsg, err)
	}
}

func TestImportTransportRequest_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json response"))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	_, err := client.ImportTransportRequest(context.Background(), 1, []uint{100})

	if err == nil {
		t.Error("Expected JSON unmarshal error, got nil")
	}
}

func TestImportTransportRequest_RequestBody(t *testing.T) {
	var receivedBody ReqImportTransportRequests

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("Failed to decode request body: %v", err)
		}

		resp := ReqImportTransportResp{ActionID: 99999}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	expectedTRs := []uint{100, 200, 300}
	_, err := client.ImportTransportRequest(context.Background(), 5, expectedTRs)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(receivedBody.TransportRequests) != len(expectedTRs) {
		t.Errorf("Expected %d transport requests, got %d", len(expectedTRs), len(receivedBody.TransportRequests))
	}
	for i, tr := range expectedTRs {
		if receivedBody.TransportRequests[i] != tr {
			t.Errorf("Expected transport request ID %d at index %d, got %d", tr, i, receivedBody.TransportRequests[i])
		}
	}
}

func TestImportTransportRequest_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		resp := ReqImportTransportResp{ActionID: 12345}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	client := createTestClient(server.URL)

	// Cancel the context after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	_, err := client.ImportTransportRequest(ctx, 1, []uint{100})

	if err == nil {
		t.Error("Expected context cancelled error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected Canceled error, got %v", err)
	}
}

// =============================================================================
// GetNodes Tests
// =============================================================================

func TestGetNodes_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}

		resp := testTMSNodesResp{
			Nodes: []testTransportNode{
				{ID: 1, Name: "DEV"},
				{ID: 2, Name: "QA"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	nodes, err := client.GetNodes(context.Background())

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("Expected 2 nodes, got %d", len(nodes))
	}
}

func TestGetNodes_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		resp := testTMSNodesResp{Nodes: []testTransportNode{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.GetNodes(shortCtx)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

// =============================================================================
// GetActionResult Tests
// =============================================================================

func TestGetActionResult_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ActionResultResp{
			ID:      123,
			Status:  "succeeded",
			EndedAt: "2024-01-01T12:00:00Z",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	status, endedAt, err := client.GetActionResult(context.Background(), 123)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if status != "succeeded" {
		t.Errorf("Expected status 'succeeded', got '%s'", status)
	}
	if endedAt != "2024-01-01T12:00:00Z" {
		t.Errorf("Expected endedAt '2024-01-01T12:00:00Z', got '%s'", endedAt)
	}
}

func TestGetActionResult_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		resp := ActionResultResp{Status: "succeeded"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, _, err := client.GetActionResult(shortCtx, 123)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

func TestWarnLogsInTransportLog_CollectsSeverityW(t *testing.T) {
	// TMS only returns W/F severity in entity-level messages; action-level
	// messages are informational (severity "I"). The test mirrors real TMS
	// responses: W messages only appear under entities[].messages.
	logJSON := `{
		"logs": [{
			"actionId": 1,
			"actionType": "IMPORT",
			"status": "DONE",
			"actionStartedAt": "2026-03-18T06:53:00Z",
			"actionTriggeredBy": "user",
			"messages": [
				{"id": 1, "messageId": " ", "severity": "I", "message": "action-level info", "createdAt": "2026-03-18T06:53:50.066Z"}
			],
			"entities": [{
				"id": 1,
				"uri": "",
				"status": "",
				"fileName": "",
				"messages": [
					{"id": 2, "messageId": " ", "severity": "W", "message": "entity warn 1", "createdAt": "2026-03-18T06:53:51.066Z"},
					{"id": 3, "messageId": " ", "severity": "I", "message": "entity info", "createdAt": "2026-03-18T06:53:52.066Z"},
					{"id": 4, "messageId": " ", "severity": "W", "message": "entity warn 2", "createdAt": "2026-03-18T06:53:53.066Z"}
				]
			}]
		}]
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(logJSON))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	out, err := client.WarnLogsInTransportLog(context.Background(), "TR123", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 warning lines, got %d: %v", len(out), out)
	}
	if !containsSubstring(out[0], "entity warn 1") || !containsSubstring(out[1], "entity warn 2") {
		t.Fatalf("unexpected messages: %v", out)
	}
}

// =============================================================================
// Helper functions
// =============================================================================

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
