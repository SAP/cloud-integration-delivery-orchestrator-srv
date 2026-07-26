package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"mmt-delivery/db"
	gh "mmt-delivery/pkg/github"

	"github.com/gin-gonic/gin"
)

// GetGitSnapshots returns the list of completed snapshots for a given artifact + tenant.
// GET /api/v1/gitSync/snapshots?artifactId={id}&tenantId={id}
func (h *Handler) GetGitSnapshots(ctx *gin.Context) {
	artifactID := ctx.Query("artifactId")
	tenantIDStr := ctx.Query("tenantId")

	if artifactID == "" || tenantIDStr == "" {
		Fail(ctx, http.StatusBadRequest, "query params 'artifactId' and 'tenantId' are required")
		return
	}

	tenantID, err := strconv.ParseUint(tenantIDStr, 10, 64)
	if err != nil {
		Fail(ctx, http.StatusBadRequest, "invalid 'tenantId'")
		return
	}

	snapshots, err := h.svc.GetSnapshots(artifactID, uint(tenantID))
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, snapshots)
}

// GetGitSnapshotFiles returns the normalized file content for a single snapshot.
// GET /api/v1/gitSync/snapshots/:id/files
func (h *Handler) GetGitSnapshotFiles(ctx *gin.Context) {
	idStr := ctx.Param("id")
	snapshotID, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		Fail(ctx, http.StatusBadRequest, "invalid snapshot ID")
		return
	}

	gitClient, err := h.resolveGitClient(ctx)
	if err != nil {
		Fail(ctx, http.StatusServiceUnavailable, "GitHub integration not configured: "+err.Error())
		return
	}

	resp, err := h.svc.GetSnapshotFiles(ctx.Request.Context(), uint(snapshotID), gitClient)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, resp)
}

// resolveGitClient reads GitRepoConfig from DB, resolves BTP Destination, and creates a GitArtifactClient.
func (h *Handler) resolveGitClient(ctx *gin.Context) (gh.GitArtifactClient, error) {
	var config db.GitRepoConfig
	if err := h.db.Where("enabled = ?", true).First(&config).Error; err != nil {
		return nil, fmt.Errorf("no enabled Git Repository Configuration found: %w", err)
	}
	return gh.NewGitClient(ctx.Request.Context(), gh.Provider(config.Provider), config.DestinationName, config.Owner, config.Repo, h.destSvc)
}
