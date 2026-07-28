package handler

import (
	"fmt"
	"net/http"

	"mmt-delivery/db"
	gh "mmt-delivery/pkg/github"
	"mmt-delivery/service"

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

// GET /api/v1/system/gitRepoConfig
func (h *Handler) GetGitRepoConfig(ctx *gin.Context) {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		OK(ctx, db.GitRepoConfig{})
		return
	}
	OK(ctx, config)
}

// PUT /api/v1/system/gitRepoConfig
func (h *Handler) UpsertGitRepoConfig(ctx *gin.Context) {
	var req db.GitRepoConfig
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
		return
	}
	if req.Provider == "" || req.DestinationName == "" || req.Owner == "" || req.Repo == "" {
		Fail(ctx, http.StatusBadRequest, "provider, destinationName, owner, and repo are required")
		return
	}

	var existing db.GitRepoConfig
	if err := h.db.First(&existing).Error; err != nil {
		// Create new
		if err := h.db.Create(&req).Error; err != nil {
			Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("failed to create git repo config: %s", err))
			return
		}
		OK(ctx, req)
		return
	}

	// Update existing
	existing.Provider = req.Provider
	existing.DestinationName = req.DestinationName
	existing.Owner = req.Owner
	existing.Repo = req.Repo
	existing.Enabled = req.Enabled
	if err := h.db.Save(&existing).Error; err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("failed to update git repo config: %s", err))
		return
	}
	OK(ctx, existing)
}

// GET /api/v1/system/gitRepoConfig/owners?provider=xxx&destinationName=xxx
func (h *Handler) GetGitOwners(ctx *gin.Context) {
	provider := ctx.Query("provider")
	destName := ctx.Query("destinationName")
	if provider == "" || destName == "" {
		Fail(ctx, http.StatusBadRequest, "query params 'provider' and 'destinationName' are required")
		return
	}

	client, err := gh.NewGitClient(ctx.Request.Context(), gh.Provider(provider), destName, "", "", h.destSvc)
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
	provider := ctx.Query("provider")
	destName := ctx.Query("destinationName")
	owner := ctx.Query("owner")
	ownerType := ctx.Query("ownerType")
	if provider == "" || destName == "" || owner == "" || ownerType == "" {
		Fail(ctx, http.StatusBadRequest, "query params 'provider', 'destinationName', 'owner' and 'ownerType' are required")
		return
	}

	client, err := gh.NewGitClient(ctx.Request.Context(), gh.Provider(provider), destName, "", "", h.destSvc)
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

// POST /api/v1/system/gitRepoConfig/test
func (h *Handler) TestGitRepoConnection(ctx *gin.Context) {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		OK(ctx, gin.H{"status": "error", "message": "no GitRepoConfig found"})
		return
	}

	gitClient, err := gh.NewGitClient(ctx.Request.Context(), gh.Provider(config.Provider), config.DestinationName, config.Owner, config.Repo, h.destSvc)
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
		ArtifactID  string `json:"artifactId"`
		Version     string `json:"version"`
		CpiTenantID uint   `json:"cpiTenantId"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
		return
	}
	if req.ArtifactID == "" || req.Version == "" || req.CpiTenantID == 0 {
		Fail(ctx, http.StatusBadRequest, "artifactId, version, and cpiTenantId are required")
		return
	}

	// Look up artifact info from existing operations
	var op db.ArtifactTenantOperation
	if err := h.db.Where("artifact_tech_id = ? AND artifact_version = ? AND tenant_id = ?",
		req.ArtifactID, req.Version, req.CpiTenantID).First(&op).Error; err != nil {
		Fail(ctx, http.StatusNotFound, fmt.Sprintf("no operation found for artifact %s@%s on tenant %d", req.ArtifactID, req.Version, req.CpiTenantID))
		return
	}

	// Resolve tenant name
	var tenant db.CpiTenant
	if err := h.db.First(&tenant, req.CpiTenantID).Error; err != nil {
		Fail(ctx, http.StatusBadRequest, fmt.Sprintf("tenant %d not found", req.CpiTenantID))
		return
	}

	gitClient, err := h.resolveGitClient(ctx)
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, "GitHub integration not configured: "+err.Error())
		return
	}

	syncReq := service.GitSyncRequest{
		ArtifactID:    req.ArtifactID,
		Version:       req.Version,
		PackageID:     op.PackageID,
		ArtifactType:  op.ArtifactType,
		CpiTenantID:   req.CpiTenantID,
		TenantName:    tenant.Name,
		TriggerSource: service.TriggerSourceManual,
	}

	if err := h.svc.GitSync(ctx.Request.Context(), syncReq, gitClient); err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	// Return the resulting snapshot
	var snapshot db.GitArtifactSnapshot
	h.db.Where("artifact_id = ? AND version = ? AND cpi_tenant_id = ?",
		req.ArtifactID, req.Version, req.CpiTenantID).First(&snapshot)

	OK(ctx, snapshot)
}
