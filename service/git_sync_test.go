package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/consts"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	gh "github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/github"

	"github.com/google/go-github/v89/github"
)

// --- Mock GitArtifactClient ---

type mockGitClient struct {
	tags      map[string]string // tag → commitSHA
	committed gh.FileMap        // last committed files
	commitSHA string            // SHA to return from Commit
	commitErr error
	tagErr    error
	readFiles gh.FileMap // files to return from ReadTree
	readErr   error      // error to return from ReadTree
}

func newMockGitClient() *mockGitClient {
	return &mockGitClient{
		tags:      make(map[string]string),
		commitSHA: "abc123def456",
	}
}

func (m *mockGitClient) TagExists(_ context.Context, tag string) (bool, string, error) {
	if sha, ok := m.tags[tag]; ok {
		return true, sha, nil
	}
	return false, "", nil
}

func (m *mockGitClient) Commit(_ context.Context, branch string, treePath string, files gh.FileMap, meta gh.CommitMeta) (string, error) {
	if m.commitErr != nil {
		return "", m.commitErr
	}
	m.committed = files
	return m.commitSHA, nil
}

func (m *mockGitClient) CreateTag(_ context.Context, tag string, commitSHA string) error {
	if m.tagErr != nil {
		return m.tagErr
	}
	m.tags[tag] = commitSHA
	return nil
}

func (m *mockGitClient) ReadTree(_ context.Context, commitSHA string, treePath string) (gh.FileMap, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.readFiles, nil
}

func (m *mockGitClient) ListOwners(_ context.Context) ([]gh.OwnerInfo, error) {
	return nil, nil
}

func (m *mockGitClient) ListRepos(_ context.Context, owner string, ownerType string) ([]gh.RepoInfo, error) {
	return nil, nil
}

func (m *mockGitClient) ListAccessibleRepos(_ context.Context) ([]gh.RepoInfo, error) {
	return nil, nil
}

// --- Mock CPI client for DownloadArtifactZip ---

type mockCPIForGitSync struct {
	mockCPIClient
	zipContent []byte
	zipErr     error
}

func (m *mockCPIForGitSync) DownloadArtifactZip(_ context.Context, artifactID, version string, artifactType consts.ArtifactType) ([]byte, error) {
	if m.zipErr != nil {
		return nil, m.zipErr
	}
	return m.zipContent, nil
}

// createTestZip builds a minimal ZIP file in memory with the given files.
func createTestZip(files map[string]string) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, _ := w.Create(name)
		f.Write([]byte(content))
	}
	w.Close()
	return buf.Bytes()
}

// =============================================================================
// Tests
// =============================================================================

func TestGitSync_HappyPath(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "sync-tenant")

	zipContent := createTestZip(map[string]string{
		"META-INF/MANIFEST.MF":                  "Bundle-Version: 1.0.0",
		"src/main/resources/script/test.groovy": "println 'hello'",
	})

	mockCPI := &mockCPIForGitSync{zipContent: zipContent}
	factory := func(ctx context.Context, dest string) (IntegrationService, error) {
		return mockCPI, nil
	}
	svc := newTestService(factory)

	gitClient := newMockGitClient()

	req := GitSyncRequest{
		ArtifactID:    "TestFlow",
		Version:       "1.0.0",
		PackageID:     "TestPackage",
		ArtifactType:  consts.Artifact_Type_Iflow,
		CpiTenantID:   tenant.ID,
		TenantName:    "cpi-dev",
		TriggerSource: "MANUAL",
	}

	err := svc.gitSync(context.Background(), req, gitClient)
	if err != nil {
		t.Fatalf("GitSync failed: %v", err)
	}

	// Verify tag was created
	if _, ok := gitClient.tags["tenant/cpi-dev/TestPackage/TestFlow/1.0.0"]; !ok {
		t.Error("expected tag to be created")
	}

	// Verify files were committed (ZIP content + .cpi-sync.yaml)
	if gitClient.committed == nil {
		t.Fatal("expected files to be committed")
	}
	if _, ok := gitClient.committed["META-INF/MANIFEST.MF"]; !ok {
		t.Error("expected META-INF/MANIFEST.MF in committed files")
	}
	if _, ok := gitClient.committed[".cpi-sync.yaml"]; !ok {
		t.Error("expected .cpi-sync.yaml in committed files")
	}

	// Verify snapshot in DB
	var snapshot db.GitArtifactSnapshot
	if err := testDB.Where("artifact_id = ? AND version = ?", "TestFlow", "1.0.0").First(&snapshot).Error; err != nil {
		t.Fatalf("snapshot not found in DB: %v", err)
	}
	if snapshot.Status != "completed" {
		t.Errorf("snapshot status: got %q, want completed", snapshot.Status)
	}
	if snapshot.CommitSHA != "abc123def456" {
		t.Errorf("snapshot CommitSHA: got %q, want abc123def456", snapshot.CommitSHA)
	}
}

func TestGitSync_IdempotencySkip(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "sync-idem-tenant")

	zipContent := createTestZip(map[string]string{
		"META-INF/MANIFEST.MF": "Bundle-Version: 2.0.0",
	})
	mockCPI := &mockCPIForGitSync{zipContent: zipContent}
	factory := func(ctx context.Context, dest string) (IntegrationService, error) {
		return mockCPI, nil
	}
	svc := newTestService(factory)

	gitClient := newMockGitClient()
	// Pre-set tag as existing
	gitClient.tags["tenant/cpi-dev/Pkg/Flow/2.0.0"] = "existing-sha-999"

	req := GitSyncRequest{
		ArtifactID:    "Flow",
		PackageID:     "Pkg",
		ArtifactType:  consts.Artifact_Type_Iflow,
		CpiTenantID:   tenant.ID,
		TenantName:    "cpi-dev",
		TriggerSource: "DR",
	}

	err := svc.gitSync(context.Background(), req, gitClient)
	if err != nil {
		t.Fatalf("GitSync should succeed (idempotent skip): %v", err)
	}

	// Should NOT have committed anything
	if gitClient.committed != nil {
		t.Error("expected no commit when tag already exists")
	}

	// Snapshot should be completed with existing SHA
	var snapshot db.GitArtifactSnapshot
	if err := testDB.Where("artifact_id = ? AND version = ?", "Flow", "2.0.0").First(&snapshot).Error; err != nil {
		t.Fatalf("snapshot not found: %v", err)
	}
	if snapshot.Status != "completed" {
		t.Errorf("status: got %q, want completed", snapshot.Status)
	}
	if snapshot.CommitSHA != "existing-sha-999" {
		t.Errorf("CommitSHA: got %q, want existing-sha-999", snapshot.CommitSHA)
	}
}

func TestGitSync_CommitFailure(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "sync-fail-tenant")

	zipContent := createTestZip(map[string]string{
		"META-INF/MANIFEST.MF": "Bundle-Version: 1.0.0",
		"test.txt":             "content",
	})
	mockCPI := &mockCPIForGitSync{zipContent: zipContent}
	factory := func(ctx context.Context, dest string) (IntegrationService, error) {
		return mockCPI, nil
	}
	svc := newTestService(factory)

	gitClient := newMockGitClient()
	gitClient.commitErr = fmt.Errorf("network timeout")

	req := GitSyncRequest{
		ArtifactID:    "FailFlow",
		Version:       "1.0.0",
		PackageID:     "Pkg",
		ArtifactType:  consts.Artifact_Type_Sc,
		CpiTenantID:   tenant.ID,
		TenantName:    "cpi-dev",
		TriggerSource: "CRON",
	}

	err := svc.gitSync(context.Background(), req, gitClient)
	if err == nil {
		t.Fatal("expected error from GitSync")
	}

	// Snapshot should be failed
	var snapshot db.GitArtifactSnapshot
	testDB.Where("artifact_id = ? AND version = ?", "FailFlow", "1.0.0").First(&snapshot)
	if snapshot.Status != "failed" {
		t.Errorf("status: got %q, want failed", snapshot.Status)
	}
	if snapshot.Error == "" {
		t.Error("expected error message in snapshot")
	}
}

func TestGitSync_TagCreationFailure(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "sync-tagfail-tenant")

	zipContent := createTestZip(map[string]string{
		"META-INF/MANIFEST.MF": "Bundle-Version: 3.0.0",
		"test.txt":             "content",
	})
	mockCPI := &mockCPIForGitSync{zipContent: zipContent}
	factory := func(ctx context.Context, dest string) (IntegrationService, error) {
		return mockCPI, nil
	}
	svc := newTestService(factory)

	gitClient := newMockGitClient()
	gitClient.tagErr = fmt.Errorf("ref already exists")

	req := GitSyncRequest{
		ArtifactID:    "TagFailFlow",
		Version:       "3.0.0",
		PackageID:     "Pkg",
		ArtifactType:  consts.Artifact_Type_Iflow,
		CpiTenantID:   tenant.ID,
		TenantName:    "cpi-dev",
		TriggerSource: "IMPORT",
	}

	err := svc.gitSync(context.Background(), req, gitClient)
	if err == nil {
		t.Fatal("expected error from GitSync when tag creation fails")
	}

	// Snapshot should be failed but CommitSHA should be recorded (commit succeeded)
	var snapshot db.GitArtifactSnapshot
	testDB.Where("artifact_id = ? AND version = ?", "TagFailFlow", "3.0.0").First(&snapshot)
	if snapshot.Status != "failed" {
		t.Errorf("status: got %q, want failed", snapshot.Status)
	}
	if snapshot.CommitSHA == "" {
		t.Error("CommitSHA should be set (commit succeeded before tag failed)")
	}
}

func TestGitSync_CpiSyncYamlContent(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "sync-yaml-tenant")

	zipContent := createTestZip(map[string]string{
		"META-INF/MANIFEST.MF": "Bundle-Version: 5.1.0",
		"file.txt":             "data",
	})
	mockCPI := &mockCPIForGitSync{zipContent: zipContent}
	factory := func(ctx context.Context, dest string) (IntegrationService, error) {
		return mockCPI, nil
	}
	svc := newTestService(factory)

	gitClient := newMockGitClient()
	drID := uint(42)

	req := GitSyncRequest{
		ArtifactID:        "YamlFlow",
		Version:           "5.1.0",
		PackageID:         "YamlPkg",
		ArtifactType:      consts.Artifact_Type_Iflow,
		CpiTenantID:       tenant.ID,
		TenantName:        "cpi-test",
		TriggerSource:     "DR",
		DeliveryRequestID: &drID,
	}

	if err := svc.gitSync(context.Background(), req, gitClient); err != nil {
		t.Fatalf("GitSync failed: %v", err)
	}

	yamlContent, ok := gitClient.committed[".cpi-sync.yaml"]
	if !ok {
		t.Fatal(".cpi-sync.yaml not found in committed files")
	}

	content := string(yamlContent)
	checks := []string{
		"artifactId: YamlFlow",
		"packageId: YamlPkg",
		"version: 5.1.0",
		"tenant: cpi-test",
		"triggerSource: DR",
		"deliveryRequestId: 42",
		"# This file is auto-generated by CPI Delivery.",
	}
	for _, check := range checks {
		if !bytes.Contains(yamlContent, []byte(check)) {
			t.Errorf(".cpi-sync.yaml missing expected content %q\nFull content:\n%s", check, content)
		}
	}
}

// =============================================================================
// Orphaned snapshot read resilience (RFC 010 · 13)
// =============================================================================

// ghErr builds a go-github *ErrorResponse carrying an HTTP status, wrapped the
// same way ReadTree wraps it (%w), so classification exercises the errors.As chain.
func ghErr(code int) error {
	return fmt.Errorf("get tree at deadbeef: %w", &github.ErrorResponse{
		Response: &http.Response{StatusCode: code},
	})
}

// seedCompletedSnapshot inserts a completed snapshot row for GetSnapshotFiles tests.
func seedCompletedSnapshot(t *testing.T, tenantID uint) db.GitArtifactSnapshot {
	t.Helper()
	snap := db.GitArtifactSnapshot{
		ArtifactID:  "OrphanFlow",
		Version:     "1.0.0",
		CpiTenantID: tenantID,
		PackageID:   "OrphanPkg",
		TreePath:    "tenant/cpi-dev/OrphanPkg/OrphanFlow",
		Status:      GitSnapshotCompleted,
		CommitSHA:   "7fe2b86db6270e822f10747a25be2edba91ab94f",
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	return snap
}

func TestGetSnapshotFiles_ErrorClassification(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "orphan-tenant")
	svc := newTestService(func(context.Context, string) (IntegrationService, error) { return nil, nil })

	cases := []struct {
		name       string
		readErr    error
		wantOrphan bool
	}{
		{"empty repo 409", ghErr(http.StatusConflict), true},
		{"commit unreachable 404", ghErr(http.StatusNotFound), true},
		{"invalid SHA 422", ghErr(http.StatusUnprocessableEntity), true},
		{"transient 500 not orphan", ghErr(http.StatusInternalServerError), false},
		{"rate limit 429 not orphan", ghErr(http.StatusTooManyRequests), false},
		{"auth 401 not orphan", ghErr(http.StatusUnauthorized), false},
		{"auth 403 not orphan", ghErr(http.StatusForbidden), false},
		{"non-github error not orphan", errors.New("dial tcp: connection refused"), false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			snap := seedCompletedSnapshot(t, tenant.ID)
			defer testDB.Unscoped().Delete(&db.GitArtifactSnapshot{}, snap.ID)

			client := newMockGitClient()
			client.readErr = tt.readErr

			_, err := svc.GetSnapshotFiles(context.Background(), snap.ID, client)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var orphan *OrphanedSnapshotError
			if got := errors.As(err, &orphan); got != tt.wantOrphan {
				t.Errorf("errors.As(OrphanedSnapshotError) = %v, want %v (err=%v)", got, tt.wantOrphan, err)
			}
		})
	}
}

func TestInvalidateSnapshot(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "invalidate-tenant")
	svc := newTestService(func(context.Context, string) (IntegrationService, error) { return nil, nil })

	t.Run("completed becomes failed", func(t *testing.T) {
		snap := seedCompletedSnapshot(t, tenant.ID)
		defer testDB.Unscoped().Delete(&db.GitArtifactSnapshot{}, snap.ID)

		if err := svc.InvalidateSnapshot(snap.ID); err != nil {
			t.Fatalf("InvalidateSnapshot: %v", err)
		}
		var got db.GitArtifactSnapshot
		testDB.First(&got, snap.ID)
		if got.Status != GitSnapshotFailed {
			t.Errorf("status = %q, want %q", got.Status, GitSnapshotFailed)
		}
		if got.Error == "" {
			t.Error("expected a human-readable error message on the failed row")
		}
	})

	t.Run("non-completed untouched", func(t *testing.T) {
		snap := db.GitArtifactSnapshot{
			ArtifactID: "OrphanFlow", Version: "2.0.0", CpiTenantID: tenant.ID,
			PackageID: "OrphanPkg", Status: GitSnapshotPending,
		}
		if err := testDB.Create(&snap).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
		defer testDB.Unscoped().Delete(&db.GitArtifactSnapshot{}, snap.ID)

		if err := svc.InvalidateSnapshot(snap.ID); err != nil {
			t.Fatalf("InvalidateSnapshot: %v", err)
		}
		var got db.GitArtifactSnapshot
		testDB.First(&got, snap.ID)
		if got.Status != GitSnapshotPending {
			t.Errorf("status = %q, want %q (pending must be left alone)", got.Status, GitSnapshotPending)
		}
	})
}
