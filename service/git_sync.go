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
	"mmt-delivery/pkg/lifecycle"
	cpiotel "mmt-delivery/pkg/otel"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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
	ctx, span := cpiotel.Tracer().Start(ctx, "git_sync")
	span.SetAttributes(
		attribute.String("artifact_id", req.ArtifactID),
		attribute.String("version", req.Version),
		attribute.String("tenant", req.TenantName),
		attribute.String("trigger_source", req.TriggerSource),
	)
	start := time.Now()
	defer func() {
		span.End()
		cpiotel.GitSyncDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("trigger_source", req.TriggerSource)))
	}()

	branch := "tenant/" + req.TenantName
	treePath := fmt.Sprintf("%s/%s", req.PackageID, req.ArtifactID)
	tagName := fmt.Sprintf("tenant/%s/%s/%s/%s", req.TenantName, req.PackageID, req.ArtifactID, req.Version)

	// Step 1: Atomically claim the snapshot (DB row = distributed lock)
	// Only the goroutine that successfully claims proceeds with steps 2-7.
	var snapshot db.GitArtifactSnapshot
	claimed, err := s.claimGitSyncSnapshot(req, branch, treePath, tagName, &snapshot)
	if err != nil {
		return err
	}
	if !claimed {
		// Already completed or in-progress by another instance
		return nil
	}

	completeSnapshot := func(status string, commitSHA string, errMsg string) {
		now := time.Now()
		s.DB.Model(&snapshot).Select(
			"Status", "CommitSHA", "CompletedAt", "Error",
		).Updates(db.GitArtifactSnapshot{
			Status:      status,
			CommitSHA:   commitSHA,
			CompletedAt: &now,
			Error:       errMsg,
		})
		if status == GitSnapshotFailed {
			cpiotel.GitSyncTotal.Add(ctx, 1, metric.WithAttributes(
				attribute.String("trigger_source", req.TriggerSource),
				attribute.String("status", "failed")))
		}
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
		cpiotel.GitSyncTotal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("trigger_source", req.TriggerSource),
			attribute.String("status", "skipped")))
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
	cpiotel.GitSyncTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("trigger_source", req.TriggerSource),
		attribute.String("status", "completed")))
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

// claimGitSyncSnapshot atomically claims ownership of a sync operation.
// Returns (true, nil) if this goroutine owns the snapshot and should proceed.
// Returns (false, nil) if already completed or another instance is processing.
// Returns (false, err) on unexpected errors.
func (s *Service) claimGitSyncSnapshot(req GitSyncRequest, branch, treePath, tagName string, out *db.GitArtifactSnapshot) (bool, error) {
	// Attempt 1: Create new snapshot (status=pending)
	*out = db.GitArtifactSnapshot{
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
	if err := s.DB.Create(out).Error; err == nil {
		return true, nil // Created — we own it
	}

	// Create failed (unique constraint). Query existing record.
	if err := s.DB.Where("artifact_id = ? AND version = ? AND cpi_tenant_id = ?",
		req.ArtifactID, req.Version, req.CpiTenantID).First(out).Error; err != nil {
		return false, fmt.Errorf("query existing snapshot: %w", err)
	}

	switch out.Status {
	case GitSnapshotCompleted:
		return false, nil // Already done
	case GitSnapshotPending:
		return false, nil // Another instance is processing
	case GitSnapshotFailed:
		// Attempt atomic claim: update only if still failed
		result := s.DB.Model(out).
			Where("status = ?", GitSnapshotFailed).
			Select("Status", "TriggeredAt", "CompletedAt", "Error", "CommitSHA",
				"TriggerSource", "DeliveryRequestID", "ArtifactOpID").
			Updates(db.GitArtifactSnapshot{
				Status:            GitSnapshotPending,
				TriggeredAt:       time.Now(),
				TriggerSource:     req.TriggerSource,
				DeliveryRequestID: req.DeliveryRequestID,
				ArtifactOpID:      req.ArtifactOpID,
			})
		if result.RowsAffected == 0 {
			return false, nil // Another instance claimed it first
		}
		return true, nil // We claimed it
	default:
		return false, fmt.Errorf("unexpected snapshot status: %s", out.Status)
	}
}

// =============================================================================
// Snapshot Read (files API)
// =============================================================================

// SnapshotFileEntry represents a single file's content in the response.
type SnapshotFileEntry struct {
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	IsBinary bool   `json:"isBinary"`
	Size     int    `json:"size"`
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

	return &SnapshotFilesResponse{
		SnapshotID: snapshot.ID,
		ArtifactID: snapshot.ArtifactID,
		Version:    snapshot.Version,
		Tenant:     tenant.Name,
		Files:      buildFileEntries(files),
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

func buildFileEntries(files gh.FileMap) []SnapshotFileEntry {
	var entries []SnapshotFileEntry
	for path, content := range files {
		if path == ".cpi-sync.yaml" {
			continue
		}
		if isBinaryPath(path) {
			entries = append(entries, SnapshotFileEntry{Path: path, IsBinary: true, Size: len(content)})
			continue
		}
		content = normalizeLineEndings(content)
		entries = append(entries, SnapshotFileEntry{
			Path:    path,
			Content: string(content),
			Size:    len(content),
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

// =============================================================================
// Trigger Helper
// =============================================================================

// TriggerGitSyncForOp resolves GitRepoConfig + git client, then executes sync synchronously.
// Returns nil if git sync is not enabled (silently skips).
// Writes a Condition to the delivery request on success or failure.
func (s *Service) TriggerGitSyncForOp(ctx context.Context, op db.ArtifactTenantOperation, triggerSource string, drID *uint) error {
	// Check if git sync is enabled
	var config db.GitRepoConfig
	if err := s.DB.Where("enabled = ?", true).First(&config).Error; err != nil {
		return nil // git sync not configured, silently skip
	}

	// Resolve tenant name
	var tenant db.CpiTenant
	if err := s.DB.First(&tenant, op.TenantID).Error; err != nil {
		return fmt.Errorf("git sync: tenant %d not found: %w", op.TenantID, err)
	}

	// Create git client
	gitClient, err := gh.NewGitClient(ctx, gh.Provider(config.Provider), config.DestinationName, config.Owner, config.Repo, s.ProviderDest)
	if err != nil {
		s.writeGitSyncCondition(drID, op.ID, lifecycle.CondError,
			fmt.Sprintf("git sync failed for %s@%s: %s", op.ArtifactTechID, op.ArtifactVersion, err))
		return fmt.Errorf("git sync: failed to create git client: %w", err)
	}

	req := GitSyncRequest{
		ArtifactID:        op.ArtifactTechID,
		Version:           op.ArtifactVersion,
		PackageID:         op.PackageID,
		ArtifactType:      op.ArtifactType,
		CpiTenantID:       op.TenantID,
		TenantName:        tenant.Name,
		TriggerSource:     triggerSource,
		DeliveryRequestID: drID,
		ArtifactOpID:      &op.ID,
	}

	if err := s.GitSync(ctx, req, gitClient); err != nil {
		s.writeGitSyncCondition(drID, op.ID, lifecycle.CondError,
			fmt.Sprintf("git sync failed for %s@%s on tenant %s: %s", op.ArtifactTechID, op.ArtifactVersion, tenant.Name, err))
		return err
	}

	s.writeGitSyncCondition(drID, op.ID, lifecycle.CondSuccess,
		fmt.Sprintf("git sync completed for %s@%s on tenant %s → %s/%s", op.ArtifactTechID, op.ArtifactVersion, tenant.Name, config.Owner, config.Repo))
	return nil
}

func (s *Service) writeGitSyncCondition(drID *uint, opID uint, state lifecycle.ConditionState, message string) {
	if drID == nil {
		return
	}
	_ = s.BatchInsertConditions([]db.Condition{{
		DeliveryRequestID:         *drID,
		ArtifactTenantOperationID: opID,
		State:                     state,
		Message:                   message,
	}})
}
