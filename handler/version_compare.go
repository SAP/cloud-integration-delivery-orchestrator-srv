package handler

import (
	"net/http"
	"strconv"

	"mmt-delivery/consts"
	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
)

// TriggerVersionCompareHandler handles POST /api/v1/deliveryRule/:id/versionCompare/trigger
func (h *Handler) TriggerVersionCompareHandler(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}

	triggeredBy := service.UserEmail(ctx)
	result, err := h.svc.TriggerVersionCompare(uint(id), triggeredBy)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	switch result.Status {
	case consts.TriggerStatusRateLimited:
		ctx.JSON(http.StatusTooManyRequests, gin.H{"status": "fail", "code": 429, "result": result})
	case consts.TriggerStatusConflict:
		ctx.JSON(http.StatusConflict, gin.H{"status": "fail", "code": 409, "result": result})
	default:
		ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": result})
	}
}

// QueryVersionCompareHandler handles GET /api/v1/deliveryRule/:id/versionCompare
func (h *Handler) QueryVersionCompareHandler(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
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
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": resp})
}

// VersionCompareSummaryHandler handles GET /api/v1/versionCompare/summary
func (h *Handler) VersionCompareSummaryHandler(ctx *gin.Context) {
	items, err := h.svc.GetVersionCompareSummary()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": items})
}

// VersionCompareCountsHandler handles GET /api/v1/versionCompare/counts
func (h *Handler) VersionCompareCountsHandler(ctx *gin.Context) {
	counts, err := h.svc.GetVersionCompareCounts()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": counts})
}
