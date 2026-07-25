package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"mmt-delivery/db"
	gh "mmt-delivery/pkg/github"
)

// SnapshotFileEntry represents a single file's content in the response.
type SnapshotFileEntry struct {
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	IsBinary bool   `json:"isBinary"`
}

// SnapshotFilesResponse is the response for GET /gitSync/snapshots/:id/files.
type SnapshotFilesResponse struct {
	SnapshotID uint                `json:"snapshotId"`
	ArtifactID string              `json:"artifactId"`
	Version    string              `json:"version"`
	Tenant     string              `json:"tenant"`
	Files      []SnapshotFileEntry `json:"files"`
}

// GetSnapshotFiles reads files from GitHub for a given snapshot and returns content.
func (s *Service) GetSnapshotFiles(ctx context.Context, snapshotID uint, gitClient gh.GitArtifactClient) (*SnapshotFilesResponse, error) {
	var snapshot db.GitArtifactSnapshot
	if err := s.DB.First(&snapshot, snapshotID).Error; err != nil {
		return nil, fmt.Errorf("snapshot %d not found: %w", snapshotID, err)
	}
	if snapshot.Status != GitSnapshotCompleted {
		return nil, fmt.Errorf("snapshot %d is not completed (status=%s)", snapshotID, snapshot.Status)
	}

	var tenant db.CpiTenant
	s.DB.First(&tenant, snapshot.CpiTenantID)

	files, err := gitClient.ReadTree(ctx, snapshot.CommitSHA, snapshot.TreePath)
	if err != nil {
		return nil, fmt.Errorf("read tree: %w", err)
	}

	entries := buildFileEntries(files)

	return &SnapshotFilesResponse{
		SnapshotID: snapshot.ID,
		ArtifactID: snapshot.ArtifactID,
		Version:    snapshot.Version,
		Tenant:     tenant.Name,
		Files:      entries,
	}, nil
}

// GetSnapshots returns the list of completed snapshots for a given artifact + tenant.
func (s *Service) GetSnapshots(artifactID string, tenantID uint) ([]db.GitArtifactSnapshot, error) {
	var snapshots []db.GitArtifactSnapshot
	if err := s.DB.
		Where("artifact_id = ? AND cpi_tenant_id = ? AND status = ?", artifactID, tenantID, GitSnapshotCompleted).
		Order("triggered_at DESC").
		Find(&snapshots).Error; err != nil {
		return nil, fmt.Errorf("query snapshots: %w", err)
	}
	return snapshots, nil
}

// buildFileEntries converts a FileMap to response entries with line ending normalization only.
func buildFileEntries(files gh.FileMap) []SnapshotFileEntry {
	var entries []SnapshotFileEntry
	for path, content := range files {
		if path == ".cpi-sync.yaml" {
			continue
		}

		if isBinaryPath(path) {
			entries = append(entries, SnapshotFileEntry{Path: path, IsBinary: true})
			continue
		}

		content = normalizeLineEndings(content)

		entries = append(entries, SnapshotFileEntry{
			Path:    path,
			Content: string(content),
		})
	}
	return entries
}

func normalizeLineEndings(content []byte) []byte {
	return bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
}

func isBinaryPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".jar") ||
		strings.HasSuffix(lower, ".zip") ||
		strings.HasSuffix(lower, ".class")
}
