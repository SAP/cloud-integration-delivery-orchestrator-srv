package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"mmt-delivery/consts"
	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
)

// TriggerVersionCompareHandler handles POST /api/v1/deliveryRule/:id/versionCompare/trigger
func (h *Handler) TriggerVersionCompareHandler(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		Fail(ctx, http.StatusBadRequest, "invalid id")
		return
	}

	triggeredBy := service.UserEmail(ctx)
	result, err := h.svc.TriggerVersionCompare(uint(id), triggeredBy)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	switch result.Status {
	case consts.TriggerStatusRateLimited:
		FailErrors(ctx, http.StatusTooManyRequests, "rate limited", result)
	case consts.TriggerStatusConflict:
		FailErrors(ctx, http.StatusConflict, "conflict", result)
	default:
		OK(ctx, result)
	}
}

// QueryVersionCompareHandler handles GET /api/v1/deliveryRule/:id/versionCompare
func (h *Handler) QueryVersionCompareHandler(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		Fail(ctx, http.StatusBadRequest, "invalid id")
		return
	}

	params := service.VersionCompareQueryParams{
		PackageIDs:   service.ParsePackageIDs(ctx.Query("packageIDs")),
		DesignTime:   ctx.Query("designTime") != "false",
		RunTime:      ctx.Query("runTime") != "false",
		MismatchOnly: ctx.Query("mismatchOnly") == "true",
	}

	resp, err := h.svc.QueryVersionCompare(uint(id), params)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, resp)
}

// VersionCompareSummaryHandler handles GET /api/v1/versionCompare/summary
func (h *Handler) VersionCompareSummaryHandler(ctx *gin.Context) {
	items, err := h.svc.GetVersionCompareSummary()
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, items)
}

// VersionCompareCountsHandler handles GET /api/v1/versionCompare/counts
func (h *Handler) VersionCompareCountsHandler(ctx *gin.Context) {
	counts, err := h.svc.GetVersionCompareCounts()
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, counts)
}

// IncludedPackagesHandler handles GET /api/v1/versionCompare/includedPackages
func (h *Handler) IncludedPackagesHandler(ctx *gin.Context) {
	packages, err := h.svc.GetIncludedPackages()
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, gin.H{"packages": packages})
}

// UpdateIncludedPackagesHandler handles PUT /api/v1/versionCompare/includedPackages
func (h *Handler) UpdateIncludedPackagesHandler(ctx *gin.Context) {
	var body struct {
		Packages []service.IncludedPackageInput `json:"packages"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	updatedBy := service.UserEmail(ctx)
	packages, err := h.svc.UpdateIncludedPackages(body.Packages, updatedBy)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OK(ctx, gin.H{"packages": packages})
}

// HandlePreviewDRFromMismatch handles GET /api/v1/deliveryRule/:id/versionCompare/previewDR
func (h *Handler) HandlePreviewDRFromMismatch(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		Fail(ctx, http.StatusBadRequest, "invalid id")
		return
	}

	resp, err := h.svc.PreviewDRFromMismatch(uint(id))
	if err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "not found"):
			Fail(ctx, http.StatusNotFound, errMsg)
		case strings.Contains(errMsg, "no design-time mismatches"):
			Fail(ctx, http.StatusConflict, errMsg)
		default:
			Fail(ctx, http.StatusInternalServerError, errMsg)
		}
		return
	}

	OK(ctx, resp)
}

// HandleCreateDRFromMismatch handles POST /api/v1/deliveryRule/:id/versionCompare/createDR
func (h *Handler) HandleCreateDRFromMismatch(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		Fail(ctx, http.StatusBadRequest, "invalid id")
		return
	}

	var req service.CreateDRFromMismatchRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	user := service.UserID(ctx)
	resp, err := h.svc.CreateDRFromMismatch(ctx.Request.Context(), uint(id), req, user)
	if err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "not found"):
			Fail(ctx, http.StatusNotFound, errMsg)
		case strings.Contains(errMsg, "completedAt mismatch"):
			Fail(ctx, http.StatusConflict, errMsg)
		case strings.Contains(errMsg, "artifactKeys must not be empty"),
			strings.Contains(errMsg, "jira link is required"),
			strings.Contains(errMsg, "no artifacts passed validation"):
			FailErrors(ctx, http.StatusBadRequest, errMsg, resp.Summary)
		default:
			Fail(ctx, http.StatusInternalServerError, errMsg)
		}
		return
	}

	OK(ctx, resp)
}

// AdhocVersionCompare handles POST /api/v1/versionCompare/adhoc
func (h *Handler) AdhocVersionCompare(c *gin.Context) {
	var req struct {
		TenantIDs []uint `json:"tenantIDs" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.TenantIDs) < 2 {
		Fail(c, http.StatusBadRequest, "at least 2 tenants required")
		return
	}

	resp, err := h.svc.AdhocVersionCompare(c.Request.Context(), req.TenantIDs)
	if err != nil {
		if err.Error() == "rate_limited" {
			Fail(c, http.StatusTooManyRequests, fmt.Sprintf("adhoc version compare is rate limited, please wait %v", service.AdhocCooldown))
			return
		}
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, resp)
}
