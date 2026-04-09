package handler

import (
	"github.com/gin-gonic/gin"
)

// PreviewBootstrap triggers a read-only inspection of the tenant's local
// prerequisites and returns a preview of what would be created by ApplyBootstrap.
// Runs synchronously (typically < 5 s).
//
// POST /api/v1/cpiTenant/:id/bootstrap/preview
//
// TODO (Phase 2): implement TenantInspector and wire result into a bootstrap job.
func (h *Handler) PreviewBootstrap(ctx *gin.Context) {
	Fail(ctx, 501, "not implemented — Phase 2")
}

// ApplyBootstrap creates all missing local prerequisites identified by a prior
// preview, then registers the TMS source node.  Runs asynchronously; returns
// the job ID immediately.  Poll GetBootstrapStatus for progress.
//
// POST /api/v1/cpiTenant/:id/bootstrap/apply
//
// TODO (Phase 2): implement TenantBootstrapper and async job execution.
func (h *Handler) ApplyBootstrap(ctx *gin.Context) {
	Fail(ctx, 501, "not implemented — Phase 2")
}

// GetBootstrapStatus returns the most recent TenantBootstrapJob for the tenant.
//
// GET /api/v1/cpiTenant/:id/bootstrap/status
//
// TODO (Phase 2): query TenantBootstrapJob by CpiTenantID, return latest.
func (h *Handler) GetBootstrapStatus(ctx *gin.Context) {
	Fail(ctx, 501, "not implemented — Phase 2")
}

// RetryBootstrap resumes a failed or waiting bootstrap job from the step it
// last failed at.  Runs asynchronously; returns the new job ID immediately.
//
// POST /api/v1/cpiTenant/:id/bootstrap/retry
//
// TODO (Phase 2): implement retry logic in TenantBootstrapper.
func (h *Handler) RetryBootstrap(ctx *gin.Context) {
	Fail(ctx, 501, "not implemented — Phase 2")
}
