package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/consts"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
	gh "github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/github"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	cpiotel "github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/otel"

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

// snapshotPendingTimeout defines how long a snapshot can stay "pending" before
// being considered stuck. Normal sync takes < 30s; 3 min is generous.
const snapshotPendingTimeout = 3 * time.Minute

// Git artifact snapshot statuses
const (
	GitSnapshotPending   = "pending"
	GitSnapshotCompleted = "completed"
	GitSnapshotFailed    = "failed"
	GitSnapshotNotFound  = "not_found" // artifact does not exist on this tenant (CPI 404)
)

// notFoundVersion is the sentinel version used for not_found snapshots.
// These snapshots only record "we checked, artifact absent" — version is irrelevant.
const notFoundVersion = "0.0.0"

// GitSyncRequest contains the parameters for a single artifact sync operation.
// Version is NOT set by callers — it's extracted from MANIFEST.MF after download.
type GitSyncRequest struct {
	ArtifactID        string
	Version           string // populated internally from META-INF/MANIFEST.MF
	PackageID         string
	ArtifactType      consts.ArtifactType
	CpiTenantID       uint
	TenantName        string // used for branch naming
	TriggerSource     string // DR | CRON | IMPORT | MANUAL
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

// gitSync executes the core sync flow: download → extract version → claim → commit → tag.
// Internal only — callers must use TriggerGitSync.
func (s *Service) gitSync(ctx context.Context, req GitSyncRequest, gitClient gh.GitArtifactClient) error {
	ctx, span := cpiotel.Tracer().Start(ctx, "git_sync")
	span.SetAttributes(
		attribute.String("artifact_id", req.ArtifactID),
		attribute.String("tenant", req.TenantName),
		attribute.String("trigger_source", req.TriggerSource),
	)
	start := time.Now()
	defer func() {
		span.End()
		cpiotel.GitSyncDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String("trigger_source", req.TriggerSource)))
	}()

	// Step 1: CPI Download (always Version='active')
	zipBytes, err := s.downloadArtifactZip(ctx, req)
	if err != nil {
		if isArtifactNotFound(err) {
			// Artifact does not exist on this tenant (first delivery / not yet imported).
			// Record this as a not_found snapshot so the frontend can read the state directly.
			s.L(ctx).Infow("artifact not found on tenant, recording not_found snapshot",
				"artifact", req.ArtifactID, "tenant", req.TenantName)
			s.recordNotFoundSnapshot(req)
			return nil
		}
		return fmt.Errorf("download artifact: %w", err)
	}

	// Step 2: Unzip + extract version from MANIFEST.MF
	files, err := unzipToFileMap(zipBytes)
	if err != nil {
		return fmt.Errorf("unzip: %w", err)
	}
	actualVersion, err := extractVersionFromManifest(files)
	if err != nil {
		return fmt.Errorf("extract version from MANIFEST.MF: %w", err)
	}

	// Use actual version (from MANIFEST.MF) as source of truth
	req.Version = actualVersion
	span.SetAttributes(attribute.String("version", req.Version))

	branch := "tenant/" + req.TenantName
	treePath := fmt.Sprintf("%s/%s", req.PackageID, req.ArtifactID)
	tagName := fmt.Sprintf("tenant/%s/%s/%s/%s", req.TenantName, req.PackageID, req.ArtifactID, req.Version)

	// Step 3: Atomically claim the snapshot (DB row = distributed lock)
	var snapshot db.GitArtifactSnapshot
	claimed, err := s.claimGitSyncSnapshot(req, branch, treePath, tagName, &snapshot)
	if err != nil {
		return err
	}
	if !claimed {
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

	// Step 4: Idempotency check (tag exists?)
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

	// Step 5: Generate .cpi-sync.yaml
	syncYaml, err := generateCpiSyncYaml(req)
	if err != nil {
		completeSnapshot(GitSnapshotFailed, "", err.Error())
		return fmt.Errorf("generate .cpi-sync.yaml: %w", err)
	}
	files[".cpi-sync.yaml"] = syncYaml

	// Step 6: Commit + Tag (per-branch serialization)
	lock := getBranchLock(branch)
	lock.Lock()
	defer lock.Unlock()

	commitMessage := fmt.Sprintf("Sync: %s, Version: %s\n\nTenant: %s(#%d)\nPackage: %s\nArtifact Type: %s\nTrigger: %s",
		req.ArtifactID, req.Version,
		req.TenantName, req.CpiTenantID, req.PackageID, req.ArtifactType, req.TriggerSource)
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

// downloadArtifactZip downloads the artifact ZIP from CPI design-time API (always Version='active').
func (s *Service) downloadArtifactZip(ctx context.Context, req GitSyncRequest) ([]byte, error) {
	var tenant db.CpiTenant
	if err := s.DB.First(&tenant, req.CpiTenantID).Error; err != nil {
		return nil, fmt.Errorf("tenant %d not found: %w", req.CpiTenantID, err)
	}

	cpiClient, err := s.CPI(ctx, tenant.PirApiDestinationName)
	if err != nil {
		return nil, fmt.Errorf("CPI client for tenant %s: %w", tenant.Name, err)
	}

	zipBytes, err := cpiClient.DownloadArtifactZip(ctx, req.ArtifactID, "active", req.ArtifactType)
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

// extractVersionFromManifest reads Bundle-Version from META-INF/MANIFEST.MF.
// This is the source of truth for the artifact version (CPI only serves 'active' version).
func extractVersionFromManifest(files gh.FileMap) (string, error) {
	manifest, ok := files["META-INF/MANIFEST.MF"]
	if !ok {
		return "", fmt.Errorf("META-INF/MANIFEST.MF not found in artifact ZIP")
	}
	for _, line := range strings.Split(string(manifest), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Bundle-Version:") {
			version := strings.TrimSpace(strings.TrimPrefix(line, "Bundle-Version:"))
			if version == "" {
				return "", fmt.Errorf("Bundle-Version is empty in MANIFEST.MF")
			}
			return version, nil
		}
	}
	return "", fmt.Errorf("Bundle-Version not found in MANIFEST.MF")
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
		if time.Since(out.TriggeredAt) < snapshotPendingTimeout {
			return false, nil // Still within timeout, let the owner finish
		}
		// Stale pending — reclaim atomically (same pattern as failed)
		result := s.DB.Model(out).
			Where("status = ? AND triggered_at < ?", GitSnapshotPending, time.Now().Add(-snapshotPendingTimeout)).
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
			return false, nil // Another instance reclaimed first
		}
		return true, nil
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

// OrphanedSnapshotError marks a completed snapshot whose pinned commit is no
// longer resolvable in the currently-configured repo. GitHub signals this with
// one of {404, 409, 422} on the tree read (empty repo / commit unreachable /
// invalid SHA) — all bucketed together because the recovery path is identical:
// a user-initiated Re-sync against the current repo (RFC 010 · 13, D2/D3).
type OrphanedSnapshotError struct {
	SnapshotID uint
	StatusCode int
	Err        error
}

func (e *OrphanedSnapshotError) Error() string {
	return fmt.Sprintf("snapshot %d orphaned: commit unreachable in current repo (github status %d): %v",
		e.SnapshotID, e.StatusCode, e.Err)
}

func (e *OrphanedSnapshotError) Unwrap() error { return e.Err }

// isOrphanStatus reports whether a GitHub status code means the pinned commit's
// content is unavailable in the current repo. Deliberately excludes {429,5xx}
// (transient) and {401,403} (auth) so those are not misclassified as orphaned.
func isOrphanStatus(code int) bool {
	return code == http.StatusNotFound || // 404: commit unreachable / repo gone
		code == http.StatusConflict || // 409: empty repo (no commits)
		code == http.StatusUnprocessableEntity // 422: invalid/unresolvable SHA
}

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
		// Classify by GitHub HTTP status (stable REST contract), never by
		// message text. {404,409,422} → orphaned bucket (Re-sync recovery);
		// everything else (transient/auth) falls through unchanged. RFC 010·13.
		if code, ok := gh.HTTPStatusOf(err); ok && isOrphanStatus(code) {
			return nil, &OrphanedSnapshotError{SnapshotID: snapshotID, StatusCode: code, Err: err}
		}
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

// InvalidateSnapshot marks a completed-but-orphaned snapshot as failed so the
// existing failed→pending reclaim path (claimGitSyncSnapshot) can re-push it to
// the currently-configured repo on the next Re-sync. This is the write half of
// the orphan recovery: the read path (GetSnapshotFiles) only classifies and
// never mutates state (RFC 010 · 13, D4/D5). The atomic guard (status=completed)
// makes concurrent invalidations idempotent and leaves non-completed rows alone.
func (s *Service) InvalidateSnapshot(snapshotID uint) error {
	var snapshot db.GitArtifactSnapshot
	if err := s.DB.First(&snapshot, snapshotID).Error; err != nil {
		return fmt.Errorf("snapshot %d not found: %w", snapshotID, err)
	}
	result := s.DB.Model(&snapshot).
		Where("status = ?", GitSnapshotCompleted).
		Select("Status", "Error").
		Updates(db.GitArtifactSnapshot{
			Status: GitSnapshotFailed,
			Error:  "Snapshot orphaned: commit unreachable in current repo, re-sync required",
		})
	if result.Error != nil {
		return fmt.Errorf("invalidate snapshot %d: %w", snapshotID, result.Error)
	}
	return nil
}

// GetSnapshots returns all snapshots for a given artifact + tenant (all statuses).
func (s *Service) GetSnapshots(artifactID string, tenantID uint) ([]db.GitArtifactSnapshot, error) {
	snapshots := []db.GitArtifactSnapshot{} // initialized: JSON serializes to [] not null
	if err := s.DB.
		Where("artifact_id = ? AND cpi_tenant_id = ?", artifactID, tenantID).
		Order("triggered_at DESC").
		Find(&snapshots).Error; err != nil {
		return nil, fmt.Errorf("query snapshots: %w", err)
	}
	return snapshots, nil
}

func buildFileEntries(files gh.FileMap) []SnapshotFileEntry {
	entries := []SnapshotFileEntry{} // initialized: JSON serializes to [] not null
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

// isArtifactNotFound returns true if the error wraps an HTTP 404 from the CPI download API,
// indicating the artifact does not exist on that tenant (first delivery, not yet imported).
func isArtifactNotFound(err error) bool {
	var httpErr *env.HttpResponseError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

// recordNotFoundSnapshot creates or updates a snapshot with status=not_found for the given
// artifact+tenant. Uses sentinel version "0.0.0" (real versions are never 0.0.0 in CPI).
// If a not_found row already exists, it updates the timestamp. This is idempotent.
func (s *Service) recordNotFoundSnapshot(req GitSyncRequest) {
	now := time.Now()
	snap := db.GitArtifactSnapshot{
		ArtifactID:    req.ArtifactID,
		Version:       notFoundVersion,
		CpiTenantID:   req.CpiTenantID,
		PackageID:     req.PackageID,
		ArtifactType:  req.ArtifactType,
		TriggerSource: req.TriggerSource,
		Status:        GitSnapshotNotFound,
		TriggeredAt:   now,
		CompletedAt:   &now,
	}
	if err := s.DB.Create(&snap).Error; err != nil {
		// Unique constraint hit → already exists, update timestamp
		s.DB.Model(&db.GitArtifactSnapshot{}).
			Where("artifact_id = ? AND version = ? AND cpi_tenant_id = ?", req.ArtifactID, notFoundVersion, req.CpiTenantID).
			Updates(map[string]any{"status": GitSnapshotNotFound, "triggered_at": now, "completed_at": now, "error": ""})
	}
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

// TriggerGitSync is the single entry point for triggering git sync.
// Resolves config, client, and tenant name internally. Returns nil if git sync is not configured.
func (s *Service) TriggerGitSync(ctx context.Context, artifactID, packageID string, artifactType consts.ArtifactType, tenantID uint, drID *uint) error {
	var config db.GitRepoConfig
	if err := s.DB.Where("enabled = ?", true).First(&config).Error; err != nil {
		return nil // git sync not configured
	}

	var tenant db.CpiTenant
	if err := s.DB.First(&tenant, tenantID).Error; err != nil {
		return fmt.Errorf("git sync: tenant %d not found: %w", tenantID, err)
	}

	gitClient, err := gh.NewGitClient(ctx, gh.Provider(config.Provider), config.DestinationName, config.Owner, config.Repo, gh.AuthConfig{
		Method:         gh.AuthMethod(config.AuthMethod),
		AppID:          config.GithubAppID,
		InstallationID: config.GithubInstallationID,
	}, s.ProviderDest)
	if err != nil {
		return fmt.Errorf("git sync: failed to create git client: %w", err)
	}

	req := GitSyncRequest{
		ArtifactID:        artifactID,
		PackageID:         packageID,
		ArtifactType:      artifactType,
		CpiTenantID:       tenantID,
		TenantName:        tenant.Name,
		TriggerSource:     TriggerSourceDR,
		DeliveryRequestID: drID,
	}

	return s.gitSync(ctx, req, gitClient)
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
