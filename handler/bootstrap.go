package handler

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// parseTenantID extracts and validates the :id URL parameter.
// Returns (id, true) on success; calls Fail and returns (0, false) on error.
func parseTenantID(ctx *gin.Context) (uint, bool) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		Fail(ctx, 400, "invalid tenant id")
		return 0, false
	}
	return uint(id), true
}

// PreviewBootstrap runs a read-only inspection of the tenant's local
// prerequisites and returns a BootstrapPreview describing what is present,
// what is missing, and what ApplyBootstrap would create.
//
// Requires: { "cfToken": "<bearer-token>" } in the request body.
//
// POST /api/v1/cpiTenant/:id/bootstrap/preview
func (h *Handler) PreviewBootstrap(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var body struct {
		CfToken string `json:"cfToken" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "cfToken is required")
		return
	}

	preview, err := h.svc.PreviewBootstrap(ctx.Request.Context(), tenantID, body.CfToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, preview)
}

// ApplyBootstrap creates an "apply" bootstrap job and launches it
// asynchronously.  Returns the job ID immediately; use GetBootstrapStatus
// to poll progress.
//
// Requires: { "cfToken": "<bearer-token>" } in the request body.
//
// POST /api/v1/cpiTenant/:id/bootstrap/apply
func (h *Handler) ApplyBootstrap(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var body struct {
		CfToken string `json:"cfToken" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "cfToken is required")
		return
	}

	jobID, err := h.svc.ApplyBootstrap(ctx.Request.Context(), tenantID, body.CfToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, gin.H{"jobId": jobID})
}

// GetBootstrapStatus returns the most recent TenantBootstrapJob for the tenant.
//
// GET /api/v1/cpiTenant/:id/bootstrap/status
func (h *Handler) GetBootstrapStatus(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	job, err := h.svc.GetBootstrapStatus(tenantID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "no bootstrap job found for tenant")
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, job)
}

// RetryBootstrap creates a "retry" bootstrap job that continues from the step
// where the previous job failed.  Runs asynchronously; returns the new job ID.
//
// Requires: { "cfToken": "<bearer-token>" } in the request body.
//
// POST /api/v1/cpiTenant/:id/bootstrap/retry
func (h *Handler) RetryBootstrap(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var body struct {
		CfToken string `json:"cfToken" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "cfToken is required")
		return
	}

	jobID, err := h.svc.RetryBootstrap(ctx.Request.Context(), tenantID, body.CfToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
			return
		}
		Fail(ctx, 409, err.Error())
		return
	}

	OK(ctx, gin.H{"jobId": jobID})
}

// ResetBootstrap is the operator escape hatch for a tenant stuck in readying
// state.  It marks the active running job as failed and transitions the tenant
// back to not_ready so a normal RetryBootstrap can proceed.
//
// No request body required.
//
// POST /api/v1/cpiTenant/:id/bootstrap/reset
func (h *Handler) ResetBootstrap(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	if err := h.svc.ResetBootstrap(tenantID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
			return
		}
		Fail(ctx, 409, err.Error())
		return
	}

	OK(ctx, gin.H{"tenantId": tenantID})
}
