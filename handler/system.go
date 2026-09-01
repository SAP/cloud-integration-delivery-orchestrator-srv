package handler

import (
	"fmt"
	"net/http"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	gh "mmt-delivery/pkg/github"

	"mmt-delivery/pkg/errcode"

	"github.com/gin-gonic/gin"
)

// --- Integration Config CRUD ---

// GetIntegrations returns all integration configs.
func (h *Handler) GetIntegrations(ctx *gin.Context) {
	configs, err := h.svc.GetAllIntegrationConfigs()
	if err != nil {
		FailCode(ctx, 500, errcode.Internal, fmt.Sprintf("failed to get integration configs: %s", err))
		return
	}
	OK(ctx, configs)
}

// UpdateIntegration updates DestinationName, Enabled, and Description for a given integration type.
func (h *Handler) UpdateIntegration(ctx *gin.Context) {
	integrationType := ctx.Param("type")

	var req struct {
		DestinationName string `json:"destinationName"`
		Enabled         bool   `json:"enabled"`
		Description     string `json:"description"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		FailCode(ctx, 400, errcode.InvalidInput, fmt.Sprintf("invalid request body: %s", err))
		return
	}

	config, err := h.svc.UpdateIntegrationConfig(integrationType, req.DestinationName, req.Enabled, req.Description)
	if err != nil {
		FailCode(ctx, 500, errcode.Internal, fmt.Sprintf("failed to update integration '%s': %s", integrationType, err))
		return
	}

	h.destSvc.Invalidate(req.DestinationName)
	OK(ctx, config)
}

// --- Database Info ---

func (h *Handler) GetDatabaseInfo(ctx *gin.Context) {
	sqlDB, err := h.db.DB()
	if err != nil {
		OK(ctx, gin.H{"host": "", "port": "", "dbName": "", "status": "error"})
		return
	}

	info := gin.H{"status": "ok"}
	if err := sqlDB.Ping(); err != nil {
		info["status"] = "error"
	}

	var dbName, host, port string
	row := sqlDB.QueryRow("SELECT current_database(), inet_server_addr()::text, inet_server_port()::text")
	if err := row.Scan(&dbName, &host, &port); err == nil {
		info["host"] = host
		info["port"] = port
		info["dbName"] = dbName
	}

	OK(ctx, info)
}

// --- System Connectivity Check ---

// GET /api/v1/system/connectivity/database
func (h *Handler) CheckConnectivityDatabase(ctx *gin.Context) {
	result := h.svc.CheckDatabase()
	if err := h.svc.PersistConnectivityResult(result); err != nil {
		h.logger.Errorf("failed to persist connectivity result: %s", err)
	}
	OK(ctx, result)
}

// GET /api/v1/system/connectivity/tms
func (h *Handler) CheckConnectivityTMS(ctx *gin.Context) {
	result := h.svc.CheckTMS(ctx.Request.Context())
	if err := h.svc.PersistConnectivityResult(result); err != nil {
		h.logger.Errorf("failed to persist connectivity result: %s", err)
	}
	OK(ctx, result)
}

// GET /api/v1/system/connectivity/tenants
func (h *Handler) CheckConnectivityTenants(ctx *gin.Context) {
	results := h.svc.CheckTenants(ctx.Request.Context())
	if err := h.svc.PersistConnectivityResults(results); err != nil {
		h.logger.Errorf("failed to persist connectivity results: %s", err)
	}
	OK(ctx, results)
}

// GET /api/v1/system/connectivity/integrations
func (h *Handler) CheckConnectivityIntegrations(ctx *gin.Context) {
	results := h.svc.CheckAllIntegrations(ctx.Request.Context())
	if err := h.svc.PersistConnectivityResults(results); err != nil {
		h.logger.Errorf("failed to persist connectivity results: %s", err)
	}
	OK(ctx, results)
}

// GET /api/v1/system/connectivity/integration/:type
func (h *Handler) TestIntegration(ctx *gin.Context) {
	integrationType := ctx.Param("type")
	config, err := h.svc.GetIntegrationConfig(integrationType)
	if err != nil {
		FailCode(ctx, 404, errcode.NotFound, fmt.Sprintf("integration '%s' not found", integrationType))
		return
	}
	result := h.svc.CheckIntegration(ctx.Request.Context(), *config)
	if err := h.svc.PersistConnectivityResult(result); err != nil {
		h.logger.Errorf("failed to persist connectivity result: %s", err)
	}
	OK(ctx, result)
}

// POST /api/v1/system/connectivity/all
func (h *Handler) CheckConnectivity(ctx *gin.Context) {
	report := h.svc.CheckAll(ctx.Request.Context())
	if err := h.svc.PersistConnectivityResults(report.Results); err != nil {
		h.logger.Errorf("failed to persist connectivity results: %s", err)
	}
	OK(ctx, report)
}

// GET /api/v1/system/connectivity/last
func (h *Handler) GetLastConnectivity(ctx *gin.Context) {
	report, err := h.svc.GetLastConnectivityReport()
	if err != nil {
		FailCode(ctx, http.StatusNotFound, errcode.NotFound, err.Error())
		return
	}
	OK(ctx, report)
}

// --- Git Repository Config ---

// GET /api/v1/system/gitRepoConfig/providers
func (h *Handler) GetGitProviders(ctx *gin.Context) {
	OK(ctx, gh.SupportedProviders())
}

// gitRepoConfigResponse wraps the persisted config with computed fields that
// depend on external state (destination URL → GitHub host classification).
type gitRepoConfigResponse struct {
	db.GitRepoConfig
	AppSettingsURL string `json:"appSettingsUrl,omitempty"`
}

// GET /api/v1/system/gitRepoConfig
func (h *Handler) GetGitRepoConfig(ctx *gin.Context) {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		OK(ctx, db.GitRepoConfig{})
		return
	}

	resp := gitRepoConfigResponse{GitRepoConfig: config}

	// Compute App settings URL for github_app mode (requires destination to
	// resolve GHES vs public host). Best-effort: omitted if resolution fails.
	if gh.AuthMethod(config.AuthMethod) == gh.AuthMethodGitHubApp && config.GithubAppSlug != "" {
		destURL := "https://github.com"
		if dest, err := h.destSvc.GetDestination(ctx.Request.Context(), config.DestinationName); err == nil && dest != nil && dest.URL != "" {
			destURL = dest.URL
		}
		resp.AppSettingsURL = gh.AppSettingsURL(destURL, config.GithubOwnerType, config.Owner, config.GithubAppSlug)
	}

	OK(ctx, resp)
}

// PUT /api/v1/system/gitRepoConfig
//
// Two write semantics, keyed by AuthMethod — the backend is the source of truth for the split:
//   - github_app: the connection-identity/credential fields (destinationName, owner, githubAppId,
//     githubInstallationId) are authored out-of-band by the manifest/setup callbacks; the client is
//     NOT trusted to supply or change them. Only the user-owned target selection (repo + enabled) is
//     applied — see updateGitAppTarget.
//   - pat (also empty AuthMethod, backward-compat): the client authors the full config.
func (h *Handler) UpsertGitRepoConfig(ctx *gin.Context) {
	var req db.GitRepoConfig
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
		return
	}

	if gh.AuthMethod(req.AuthMethod) == gh.AuthMethodGitHubApp {
		h.updateGitAppTarget(ctx, req)
		return
	}

	// pat mode: client authors the whole row.
	if req.Provider == "" || req.DestinationName == "" || req.Repo == "" {
		Fail(ctx, http.StatusBadRequest, "provider, destinationName, and repo are required")
		return
	}
	if req.Owner == "" {
		Fail(ctx, http.StatusBadRequest, "owner is required for Personal Access Token(PAT) mode")
		return
	}

	var existing db.GitRepoConfig
	if err := h.db.First(&existing).Error; err != nil {
		// Create new
		req.AuthMethod = string(gh.AuthMethodPAT)
		if err := h.db.Create(&req).Error; err != nil {
			Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("failed to create git repo config: %s", err))
			return
		}
		OK(ctx, gin.H{"config": req})
		return
	}

	// Repo target changed (owner / repo / destination) → warn about orphaned snapshots.
	warning := h.snapshotImpactWarning(existing.Owner != req.Owner || existing.Repo != req.Repo || existing.DestinationName != req.DestinationName)

	// Update existing as a full pat config. Switching back from github_app to pat is an explicit
	// user action, so the stale App identifiers are cleared — the row reflects a single active mode.
	existing.Provider = req.Provider
	existing.DestinationName = req.DestinationName
	existing.Owner = req.Owner
	existing.Repo = req.Repo
	existing.Enabled = req.Enabled
	existing.AuthMethod = string(gh.AuthMethodPAT)
	existing.GithubAppID = 0
	existing.GithubInstallationID = 0
	existing.GithubOwnerType = ""
	existing.GithubAppSlug = ""
	if err := h.db.Save(&existing).Error; err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("failed to update git repo config: %s", err))
		return
	}

	result := gin.H{"config": existing}
	if warning != "" {
		result["warning"] = warning
	}
	OK(ctx, result)
}

// updateGitAppTarget applies the user-owned target selection (repo + enabled) for github_app mode.
// It deliberately does NOT touch the callback-authored identity/credential fields (destinationName,
// owner, githubAppId, githubInstallationId) — those are the backend's source of truth. Requires an
// existing, fully-installed github_app row (created by the manifest/setup callbacks); otherwise 409.
func (h *Handler) updateGitAppTarget(ctx *gin.Context, req db.GitRepoConfig) {
	if req.Repo == "" {
		Fail(ctx, http.StatusBadRequest, "repo is required")
		return
	}
	var existing db.GitRepoConfig
	if err := h.db.First(&existing).Error; err != nil {
		Fail(ctx, http.StatusConflict, "GitHub App not registered yet; complete the App registration flow first")
		return
	}
	if gh.AuthMethod(existing.AuthMethod) != gh.AuthMethodGitHubApp {
		Fail(ctx, http.StatusConflict, "existing git repo config is not in github_app mode")
		return
	}
	if existing.GithubAppID == 0 || existing.GithubInstallationID == 0 {
		Fail(ctx, http.StatusConflict, "GitHub App not fully installed yet (missing app or installation id)")
		return
	}

	warning := h.snapshotImpactWarning(existing.Repo != req.Repo)

	existing.Repo = req.Repo
	existing.Enabled = req.Enabled
	if err := h.db.Save(&existing).Error; err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("failed to update git repo config: %s", err))
		return
	}

	result := gin.H{"config": existing}
	if warning != "" {
		result["warning"] = warning
	}
	OK(ctx, result)
}

// snapshotImpactWarning returns a user-facing warning when the sync target changed and completed
// snapshots exist that reference the old target (their Code Compare may break). Empty when no impact.
func (h *Handler) snapshotImpactWarning(changed bool) string {
	if !changed {
		return ""
	}
	var count int64
	h.db.Model(&db.GitArtifactSnapshot{}).Where("status = ?", "completed").Count(&count)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("Repository target changed. %d existing snapshot(s) reference the old repository — their Code Compare may become unavailable.", count)
}

// gitHubAppModeConfigured reports whether the persisted GitRepoConfig is in github_app mode.
// Returns false when no config exists yet (PAT is the default). Used to guard the PAT-only
// owner/repo browsing endpoints, which have no meaning in App mode (the installation token scopes
// discovery to the granted repos — see GetGitAppRepos).
func (h *Handler) gitHubAppModeConfigured() bool {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		return false
	}
	return gh.AuthMethod(config.AuthMethod) == gh.AuthMethodGitHubApp
}

// GET /api/v1/system/gitRepoConfig/owners?provider=xxx&destinationName=xxx
func (h *Handler) GetGitOwners(ctx *gin.Context) {
	if h.gitHubAppModeConfigured() {
		Fail(ctx, http.StatusBadRequest, "owner browsing is not available in github_app mode; use /system/gitApp/repos")
		return
	}
	provider := ctx.Query("provider")
	destName := ctx.Query("destinationName")
	if provider == "" || destName == "" {
		Fail(ctx, http.StatusBadRequest, "query params 'provider' and 'destinationName' are required")
		return
	}

	client, err := gh.NewGitClient(ctx.Request.Context(), gh.Provider(provider), destName, "", "", gh.AuthConfig{}, h.destSvc)
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, fmt.Sprintf("failed to create git client: %s", err))
		return
	}

	owners, err := client.ListOwners(ctx.Request.Context())
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, fmt.Sprintf("failed to list owners: %s", err))
		return
	}

	OK(ctx, owners)
}

// GET /api/v1/system/gitRepoConfig/repos?provider=xxx&destinationName=xxx&owner=xxx&ownerType=xxx
func (h *Handler) GetGitRepos(ctx *gin.Context) {
	if h.gitHubAppModeConfigured() {
		Fail(ctx, http.StatusBadRequest, "repo browsing is not available in github_app mode; use /system/gitApp/repos")
		return
	}
	provider := ctx.Query("provider")
	destName := ctx.Query("destinationName")
	owner := ctx.Query("owner")
	ownerType := ctx.Query("ownerType")
	if provider == "" || destName == "" || owner == "" || ownerType == "" {
		Fail(ctx, http.StatusBadRequest, "query params 'provider', 'destinationName', 'owner' and 'ownerType' are required")
		return
	}

	client, err := gh.NewGitClient(ctx.Request.Context(), gh.Provider(provider), destName, "", "", gh.AuthConfig{}, h.destSvc)
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, fmt.Sprintf("failed to create git client: %s", err))
		return
	}

	repos, err := client.ListRepos(ctx.Request.Context(), owner, ownerType)
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, fmt.Sprintf("failed to list repos: %s", err))
		return
	}

	OK(ctx, repos)
}

// GetGitAppRepos returns the repositories the current GitHub App installation was granted
// (App-mode read-back discovery, OP-1). Unlike GetGitOwners/GetGitRepos (PAT-only, browse an
// arbitrary owner), this reads the persisted GitRepoConfig and lists exactly the repos the admin
// authorized at install time via GET /installation/repositories. Requires the install step to
// have completed (GithubInstallationID populated by the setup callback).
//
// GET /api/v1/system/gitApp/repos
func (h *Handler) GetGitAppRepos(ctx *gin.Context) {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		Fail(ctx, http.StatusNotFound, "no GitRepoConfig found")
		return
	}
	if gh.AuthMethod(config.AuthMethod) != gh.AuthMethodGitHubApp {
		Fail(ctx, http.StatusBadRequest, "installation repo read-back is only valid in github_app mode")
		return
	}
	if config.GithubAppID == 0 || config.GithubInstallationID == 0 {
		Fail(ctx, http.StatusConflict, "GitHub App not fully installed yet (missing app or installation id)")
		return
	}

	// owner/repo are irrelevant for installation read-back; the installation token scopes the result.
	client, err := gh.NewGitClient(ctx.Request.Context(), gh.Provider(config.Provider), config.DestinationName, "", "", gh.AuthConfig{
		Method:         gh.AuthMethod(config.AuthMethod),
		AppID:          config.GithubAppID,
		InstallationID: config.GithubInstallationID,
	}, h.destSvc)
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, fmt.Sprintf("failed to create git client: %s", err))
		return
	}

	repos, err := client.ListAccessibleRepos(ctx.Request.Context())
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, fmt.Sprintf("failed to list accessible repos: %s", err))
		return
	}

	OK(ctx, repos)
}

// POST /api/v1/system/gitRepoConfig/test
func (h *Handler) TestGitRepoConnection(ctx *gin.Context) {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		OK(ctx, gin.H{"status": "error", "message": "no GitRepoConfig found"})
		return
	}

	gitClient, err := gh.NewGitClient(ctx.Request.Context(), gh.Provider(config.Provider), config.DestinationName, config.Owner, config.Repo, gh.AuthConfig{
		Method:         gh.AuthMethod(config.AuthMethod),
		AppID:          config.GithubAppID,
		InstallationID: config.GithubInstallationID,
	}, h.destSvc)
	if err != nil {
		OK(ctx, gin.H{"status": "error", "message": err.Error()})
		return
	}

	// Verify repo is accessible by attempting to read a non-existent tag
	_, _, err = gitClient.TagExists(ctx.Request.Context(), "__connection_test__")
	if err != nil {
		OK(ctx, gin.H{"status": "error", "message": fmt.Sprintf("cannot access repository: %s", err)})
		return
	}

	OK(ctx, gin.H{"status": "ok", "message": "Repository is accessible"})
}

// --- Git Sync Trigger ---

// POST /api/v1/gitSync/trigger
func (h *Handler) TriggerGitSync(ctx *gin.Context) {
	var req struct {
		ArtifactID   string `json:"artifactId"`
		CpiTenantID  uint   `json:"cpiTenantId"`
		ArtifactType string `json:"artifactType"`
		PackageID    string `json:"packageId"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
		return
	}
	if req.ArtifactID == "" || req.CpiTenantID == 0 || req.ArtifactType == "" || req.PackageID == "" {
		Fail(ctx, http.StatusBadRequest, "artifactId, cpiTenantId, artifactType, and packageId are required")
		return
	}

	if err := h.svc.TriggerGitSync(ctx.Request.Context(), req.ArtifactID, req.PackageID, consts.ArtifactType(req.ArtifactType), req.CpiTenantID, nil); err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, gin.H{"status": "ok"})
}
