package cpi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/pkg/env"
)

// createTestClient creates a CpiClient for testing with the given API URL
func createTestClient(apiURL string) *CpiClient {
	return &CpiClient{
		HttpClient: &env.HttpClient{
			HttpClient:  &http.Client{},
			AccessToken: "test-token",
			ApiURL:      apiURL,
			TokenExp:    time.Now().Add(24 * time.Hour),
		},
		sem: make(chan struct{}, maxConcurrentRequests),
	}
}

// =============================================================================
// GetPackages Tests
// =============================================================================

func TestGetPackages_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		if r.URL.Path != "/IntegrationPackages" {
			t.Errorf("Expected path /IntegrationPackages, got %s", r.URL.Path)
		}

		resp := PackagesResponse{
			D: struct {
				Results []CPIPackage `json:"results"`
			}{
				Results: []CPIPackage{
					{ID: "pkg1", Name: "Package 1", Version: "1.0.0"},
					{ID: "pkg2", Name: "Package 2", Version: "2.0.0"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	packages, err := client.GetPackages(context.Background())

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(packages) != 2 {
		t.Errorf("Expected 2 packages, got %d", len(packages))
	}
	if packages[0].ID != "pkg1" {
		t.Errorf("Expected first package ID 'pkg1', got '%s'", packages[0].ID)
	}
}

func TestGetPackages_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		json.NewEncoder(w).Encode(PackagesResponse{})
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.GetPackages(shortCtx)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

func TestGetPackages_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	_, err := client.GetPackages(context.Background())

	if err == nil {
		t.Error("Expected JSON unmarshal error, got nil")
	}
}

// =============================================================================
// GetPackageArtifactsByType Tests
// =============================================================================

func TestGetPackageArtifactsByType_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}

		resp := packageArtifactsResp{
			D: struct {
				Results []ArtifactCommonItem `json:"results"`
			}{
				Results: []ArtifactCommonItem{
					{ID: "iflow1", Name: "IFlow 1", Version: "1.0.0"},
					{ID: "iflow2", Name: "IFlow 2", Version: "2.0.0"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	artifacts, err := client.GetPackageArtifactsByType(context.Background(), "test-package", consts.Artifact_Type_Iflow)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(artifacts) != 2 {
		t.Errorf("Expected 2 artifacts, got %d", len(artifacts))
	}
	if artifacts[0].ID != "iflow1" {
		t.Errorf("Expected first artifact ID 'iflow1', got '%s'", artifacts[0].ID)
	}
}

func TestGetPackageArtifactsByType_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		json.NewEncoder(w).Encode(packageArtifactsResp{})
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.GetPackageArtifactsByType(shortCtx, "test-package", consts.Artifact_Type_Iflow)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

// =============================================================================
// DeployArtifact Tests
// =============================================================================

func TestDeployArtifact_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`"task-12345"`))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	taskID, err := client.DeployArtifact(context.Background(), "test-iflow", "1.0.0", consts.Artifact_Type_Iflow)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if taskID != `"task-12345"` {
		t.Errorf("Expected taskID '\"task-12345\"', got '%s'", taskID)
	}
}

func TestDeployArtifact_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.Write([]byte(`"task-12345"`))
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.DeployArtifact(shortCtx, "test-iflow", "1.0.0", consts.Artifact_Type_Iflow)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

// =============================================================================
// CheckDeployStatusByTaskID Tests
// =============================================================================

func TestCheckDeployStatusByTaskID_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}

		resp := DeployStatus{
			D: struct {
				Metadata struct {
					ID   string `json:"id"`
					URI  string `json:"uri"`
					Type string `json:"type"`
				} `json:"__metadata"`
				TaskID string `json:"TaskId"`
				Status string `json:"Status"`
			}{
				TaskID: "task-123",
				Status: "Success",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	status, err := client.CheckDeployStatusByTaskID(context.Background(), "task-123")

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if status != "Success" {
		t.Errorf("Expected status 'Success', got '%s'", status)
	}
}

func TestCheckDeployStatusByTaskID_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		json.NewEncoder(w).Encode(DeployStatus{})
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.CheckDeployStatusByTaskID(shortCtx, "task-123")

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

// =============================================================================
// GetRuntimeArtifacts Tests
// =============================================================================

func TestGetRuntimeArtifacts_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}

		resp := RuntimeArtifactsResp{
			D: struct {
				Results []RuntimeArtifact `json:"results"`
			}{
				Results: []RuntimeArtifact{
					{ID: "art1", Name: "Artifact 1", Status: "STARTED"},
					{ID: "art2", Name: "Artifact 2", Status: "ERROR"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	artifacts, err := client.GetRuntimeArtifacts(context.Background())

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(artifacts) != 2 {
		t.Errorf("Expected 2 artifacts, got %d", len(artifacts))
	}
	if artifacts[0].ID != "art1" {
		t.Errorf("Expected first artifact ID 'art1', got '%s'", artifacts[0].ID)
	}
}

func TestGetRuntimeArtifacts_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		json.NewEncoder(w).Encode(RuntimeArtifactsResp{})
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.GetRuntimeArtifacts(shortCtx)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

// =============================================================================
// ContextCancellation Tests
// =============================================================================

func TestGetPackages_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		json.NewEncoder(w).Encode(PackagesResponse{})
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	client := createTestClient(server.URL)

	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	_, err := client.GetPackages(ctx)

	if err == nil {
		t.Error("Expected context cancelled error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected Canceled error, got %v", err)
	}
}

// =============================================================================
// ImportPackage Tests
// =============================================================================

func TestImportPackage_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json;charset=UTF-8" {
			t.Errorf("Expected Content-Type application/json;charset=UTF-8, got %s", contentType)
		}

		resp := PackageResponse{
			D: CPIPackage{ID: "new-pkg", Name: "New Package", Version: "1.0.0"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	pkg, err := client.ImportPackage(context.Background(), importPackageRequest{
		ID:   "new-pkg",
		Name: "New Package",
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if pkg.ID != "new-pkg" {
		t.Errorf("Expected package ID 'new-pkg', got '%s'", pkg.ID)
	}
}

func TestImportPackage_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		json.NewEncoder(w).Encode(PackageResponse{})
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	_, err := client.ImportPackage(shortCtx, importPackageRequest{ID: "pkg"})

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}

// =============================================================================
// UndeployRuntimeArtifacts Tests
// =============================================================================

func TestUndeployRuntimeArtifacts_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("Expected DELETE method, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := createTestClient(server.URL)
	err := client.UndeployRuntimeArtifacts(context.Background(), "art-1")

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestUndeployRuntimeArtifacts_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := createTestClient(server.URL)
	err := client.UndeployRuntimeArtifacts(shortCtx, "art-1")

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}
}
