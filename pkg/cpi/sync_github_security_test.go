package cpi

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// =============================================================================
// safePathSegment Tests
// =============================================================================

func TestSafePathSegment_Valid(t *testing.T) {
	valid := []string{
		"myartifact",
		"MyArtifact_v2",
		"1.0.0",
		"artifact-name",
		"com.sap.integration.flow",
	}
	for _, s := range valid {
		if err := safePathSegment(s); err != nil {
			t.Errorf("safePathSegment(%q) = %v, want nil", s, err)
		}
	}
}

func TestSafePathSegment_Invalid(t *testing.T) {
	invalid := []struct {
		input string
		desc  string
	}{
		{"", "empty string"},
		{".", "single dot"},
		{"..", "double dot"},
		{"../etc", "path traversal with prefix"},
		{"foo/bar", "forward slash"},
		{"foo\\bar", "backslash"},
		{"..hidden", "starts with double dot"},
		{"a/../b", "embedded traversal"},
	}
	for _, tc := range invalid {
		if err := safePathSegment(tc.input); err == nil {
			t.Errorf("safePathSegment(%q) [%s] = nil, want error", tc.input, tc.desc)
		}
	}
}

// =============================================================================
// Zip Slip Protection Tests
// =============================================================================

func TestUnzipSource_ZipSlipRejected(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Override Artifact_Base_Dir and Cpi_Base_Repo for testing
	origArtifactDir := Artifact_Base_Dir
	origBaseRepo := Cpi_Base_Repo
	Artifact_Base_Dir = tmpDir
	Cpi_Base_Repo = filepath.Join(tmpDir, "repo")
	defer func() {
		Artifact_Base_Dir = origArtifactDir
		Cpi_Base_Repo = origBaseRepo
	}()

	// Create target repo directory
	os.MkdirAll(filepath.Join(Cpi_Base_Repo, "pkg1", "art1"), 0o755)

	// Create a malicious zip with path traversal
	zipPath := filepath.Join(tmpDir, "art1:1.0.0.zip")
	createMaliciousZip(t, zipPath, "../../etc/passwd")

	err := unzipSource("art1", "1.0.0", "pkg1")
	if err == nil {
		t.Fatal("unzipSource should reject zip with path traversal, got nil")
	}
	if got := err.Error(); !contains(got, "path traversal") {
		t.Errorf("expected error mentioning 'path traversal', got: %s", got)
	}
}

func TestUnzipSource_NormalFileAccepted(t *testing.T) {
	tmpDir := t.TempDir()

	origArtifactDir := Artifact_Base_Dir
	origBaseRepo := Cpi_Base_Repo
	Artifact_Base_Dir = tmpDir
	Cpi_Base_Repo = filepath.Join(tmpDir, "repo")
	defer func() {
		Artifact_Base_Dir = origArtifactDir
		Cpi_Base_Repo = origBaseRepo
	}()

	// Create a normal zip with safe paths
	zipPath := filepath.Join(tmpDir, "art1:1.0.0.zip")
	createNormalZip(t, zipPath, map[string]string{
		"src/flow.xml": "<flow/>",
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0",
	})

	err := unzipSource("art1", "1.0.0", "pkg1")
	if err != nil {
		t.Fatalf("unzipSource should accept normal zip, got: %v", err)
	}

	// Verify files were extracted
	extracted := filepath.Join(Cpi_Base_Repo, "pkg1", "art1", "src", "flow.xml")
	if _, err := os.Stat(extracted); os.IsNotExist(err) {
		t.Errorf("expected file %s to be extracted", extracted)
	}
}

func TestUnzipSource_InvalidArtifactId(t *testing.T) {
	err := unzipSource("../evil", "1.0.0", "pkg1")
	if err == nil {
		t.Fatal("unzipSource should reject invalid artifactId, got nil")
	}
}

func TestUnzipSource_InvalidVersion(t *testing.T) {
	err := unzipSource("art1", "../../bad", "pkg1")
	if err == nil {
		t.Fatal("unzipSource should reject invalid version, got nil")
	}
}

func TestUnzipSource_InvalidPackageId(t *testing.T) {
	err := unzipSource("art1", "1.0.0", "../evil")
	if err == nil {
		t.Fatal("unzipSource should reject invalid packageId, got nil")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func createMaliciousZip(t *testing.T, path, maliciousEntry string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(maliciousEntry)
	if err != nil {
		t.Fatalf("failed to create zip entry: %v", err)
	}
	w.Write([]byte("malicious content"))
	zw.Close()

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write zip file: %v", err)
	}
}

func createNormalZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		w.Write([]byte(content))
	}
	zw.Close()

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write zip file: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
