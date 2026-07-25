package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	gh "mmt-delivery/pkg/github"

	"gopkg.in/yaml.v3"
)

// Git sync trigger sources
const (
	TriggerSourceDR     = "DR"
	TriggerSourceCron   = "CRON"
	TriggerSourceImport = "IMPORT"
	TriggerSourceManual = "MANUAL"
)

// Git artifact snapshot statuses
const (
	GitSnapshotPending   = "pending"
	GitSnapshotCompleted = "completed"
	GitSnapshotFailed    = "failed"
)

// GitSyncRequest contains the parameters for a single artifact sync operation.
type GitSyncRequest struct {
	ArtifactID    string
	Version       string
	PackageID     string
	ArtifactType  consts.ArtifactType
	CpiTenantID   uint
	TenantName    string // used for branch naming
	TriggerSource string // DR | CRON | IMPORT | MANUAL
	DeliveryRequestID *uint
	ArtifactOpID      *uint
}

// branchLocks provides best-effort per-branch serialization within a single instance.
// In multi-instance deployments (e.g., CF instances: 2), this does NOT prevent cross-instance
// concurrent writes. GitHub API's Update Ref will return 422 if the branch tip has moved
// since we read it — the caller should retry in that case.
var branchLocks sync.Map // key: branch name (string), value: *sync.Mutex

func getBranchLock(branch string) *sync.Mutex {
	val, _ := branchLocks.LoadOrStore(branch, &sync.Mutex{})
	return val.(*sync.Mutex)
}

// GitSync executes the 7-step sync flow for a single artifact version.
func (s *Service) GitSync(ctx context.Context, req GitSyncRequest, gitClient gh.GitArtifactClient) error {
	branch := "tenant/" + req.TenantName
	treePath := fmt.Sprintf("packages/%s/%s", req.PackageID, req.ArtifactID)
	tagName := fmt.Sprintf("tenant/%s/%s/%s/%s", req.TenantName, req.PackageID, req.ArtifactID, req.Version)

	// Step 1: Create GitArtifactSnapshot(pending)
	snapshot := db.GitArtifactSnapshot{
		ArtifactID:        req.ArtifactID,
		Version:           req.Version,
		CpiTenantID:       req.CpiTenantID,
		PackageID:         req.PackageID,
		ArtifactType:      req.ArtifactType,
		BranchName:        branch,
		TreePath:          treePath,
		TagName:           tagName,
		TriggerSource:     req.TriggerSource,
		DeliveryRequestID: req.DeliveryRequestID,
		ArtifactOpID:      req.ArtifactOpID,
		Status:            GitSnapshotPending,
		TriggeredAt:       time.Now(),
	}
	if err := s.DB.Create(&snapshot).Error; err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}

	completeSnapshot := func(status string, commitSHA string, errMsg string) {
		now := time.Now()
		s.DB.Model(&snapshot).Updates(map[string]interface{}{
			"status":       status,
			"commit_sha":   commitSHA,
			"completed_at": &now,
			"error":        errMsg,
		})
	}

	// Step 2: Idempotency check (tag exists?)
	exists, existingSHA, err := gitClient.TagExists(ctx, tagName)
	if err != nil {
		completeSnapshot(GitSnapshotFailed, "", err.Error())
		return fmt.Errorf("tag exists check: %w", err)
	}
	if exists {
		s.L(ctx).Infow("tag already exists, skipping sync", "tag", tagName, "commitSHA", existingSHA)
		completeSnapshot(GitSnapshotCompleted, existingSHA, "")
		return nil
	}

	// Step 3: CPI Download
	zipBytes, err := s.downloadArtifactZip(ctx, req)
	if err != nil {
		completeSnapshot(GitSnapshotFailed, "", err.Error())
		return fmt.Errorf("download artifact: %w", err)
	}

	// Step 4: Unzip + generate .cpi-sync.yaml
	files, err := unzipToFileMap(zipBytes)
	if err != nil {
		completeSnapshot(GitSnapshotFailed, "", err.Error())
		return fmt.Errorf("unzip: %w", err)
	}
	syncYaml, err := generateCpiSyncYaml(req)
	if err != nil {
		completeSnapshot(GitSnapshotFailed, "", err.Error())
		return fmt.Errorf("generate .cpi-sync.yaml: %w", err)
	}
	files[".cpi-sync.yaml"] = syncYaml

	// Step 5 + 6: Commit + Tag (per-branch serialization)
	lock := getBranchLock(branch)
	lock.Lock()
	defer lock.Unlock()

	commitMessage := fmt.Sprintf("sync(%s): v%s from %s [%s]\n\nPackage: %s\nArtifact Type: %s\nTrigger: %s",
		req.ArtifactID, req.Version, req.TenantName, req.TriggerSource,
		req.PackageID, req.ArtifactType, req.TriggerSource)
	commitSHA, err := gitClient.Commit(ctx, branch, treePath, files, gh.CommitMeta{Message: commitMessage})
	if err != nil {
		completeSnapshot(GitSnapshotFailed, "", err.Error())
		return fmt.Errorf("commit: %w", err)
	}

	// Step 6: Create tag
	if err := gitClient.CreateTag(ctx, tagName, commitSHA); err != nil {
		completeSnapshot(GitSnapshotFailed, commitSHA, err.Error())
		return fmt.Errorf("create tag: %w", err)
	}

	// Step 7: Update snapshot(completed)
	completeSnapshot(GitSnapshotCompleted, commitSHA, "")
	s.L(ctx).Infow("git sync completed", "artifact", req.ArtifactID, "version", req.Version, "commit", commitSHA)
	return nil
}

// downloadArtifactZip downloads the artifact ZIP from CPI design-time API.
func (s *Service) downloadArtifactZip(ctx context.Context, req GitSyncRequest) ([]byte, error) {
	var tenant db.CpiTenant
	if err := s.DB.First(&tenant, req.CpiTenantID).Error; err != nil {
		return nil, fmt.Errorf("tenant %d not found: %w", req.CpiTenantID, err)
	}

	cpiClient, err := s.CPI(ctx, tenant.PirApiDestinationName)
	if err != nil {
		return nil, fmt.Errorf("CPI client for tenant %s: %w", tenant.Name, err)
	}

	zipBytes, err := cpiClient.DownloadArtifactZip(ctx, req.ArtifactID, req.Version, req.ArtifactType)
	if err != nil {
		return nil, err
	}
	return zipBytes, nil
}

// unzipToFileMap extracts a ZIP archive into an in-memory file map.
func unzipToFileMap(zipBytes []byte) (gh.FileMap, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	files := make(gh.FileMap)
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s in zip: %w", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s in zip: %w", f.Name, err)
		}
		// Normalize path separators
		name := strings.ReplaceAll(f.Name, "\\", "/")
		files[name] = content
	}
	return files, nil
}

// cpiSyncYaml is the structure for .cpi-sync.yaml
type cpiSyncYaml struct {
	ArtifactID        string `yaml:"artifactId"`
	PackageID         string `yaml:"packageId"`
	Type              string `yaml:"type"`
	Version           string `yaml:"version"`
	Tenant            string `yaml:"tenant"`
	SyncedAt          string `yaml:"syncedAt"`
	TriggerSource     string `yaml:"triggerSource"`
	DeliveryRequestID *uint  `yaml:"deliveryRequestId,omitempty"`
}

const cpiSyncYamlHeader = "# This file is auto-generated by CPI Delivery.\n# It records the sync context of this artifact version.\n# Do not edit manually.\n\n"

func generateCpiSyncYaml(req GitSyncRequest) ([]byte, error) {
	data := cpiSyncYaml{
		ArtifactID:        req.ArtifactID,
		PackageID:         req.PackageID,
		Type:              string(req.ArtifactType),
		Version:           req.Version,
		Tenant:            req.TenantName,
		SyncedAt:          time.Now().UTC().Format(time.RFC3339),
		TriggerSource:     req.TriggerSource,
		DeliveryRequestID: req.DeliveryRequestID,
	}
	yamlBytes, err := yaml.Marshal(data)
	if err != nil {
		return nil, err
	}
	return append([]byte(cpiSyncYamlHeader), yamlBytes...), nil
}
