package handler

// tms_node.go — TMS Node registration sync HTTP endpoints (Phase 3).
//
// All four endpoints are synchronous (no goroutines, no job records).
// Status changes are written in-request and returned directly to the caller.
//
// Routes (registered in handler.go under CpiTenant.Manage scope):
//
//	POST  /api/v1/cpiTenant/:id/tms-node/register   — start TMS node registration
//	GET   /api/v1/cpiTenant/:id/tms-node/status      — query current registration status
//	GET   /api/v1/cpiTenant/:id/tms-node/routes      — live route list from TMS
//	POST  /api/v1/cpiTenant/:id/tms-node/confirm     — operator confirms Route configured

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/service"
)

// RegisterTmsNode executes TMS Node registration synchronously.
//
// POST /api/v1/cpiTenant/:id/tms-node/register
//
// Request body:
//
//	{ "nodeId": 42, "nodeName": "DEV_NODE" }
//
// Preconditions checked here:
//   - LifecycleState = ready (bootstrap must be complete)
//   - TmsNodeRegistrationStatus != registering (prevent double-registration
//     while waiting for Route confirmation)
func (h *Handler) RegisterTmsNode(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var body struct {
		NodeId   uint   `json:"nodeId"   binding:"required"`
		NodeName string `json:"nodeName" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "nodeId and nodeName are required")
		return
	}

	// Guard: bootstrap must be complete before TMS node registration can start.
	var tenant db.CpiTenant
	if err := h.db.First(&tenant, tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	if tenant.LifecycleState != lifecycle.TenantReady {
		Fail(ctx, 400, "bootstrap must be completed (lifecycleState=ready) before TMS node registration")
		return
	}

	if err := h.svc.RegisterTmsNode(ctx.Request.Context(), tenantID, body.NodeId, body.NodeName); err != nil {
		switch {
		case errors.Is(err, service.ErrAlreadyRegistering):
			Fail(ctx, 409, err.Error())
		default:
			Fail(ctx, 500, err.Error())
		}
		return
	}

	// Re-read tenant to return updated state.
	var updated db.CpiTenant
	if err := h.db.First(&updated, tenantID).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, gin.H{
		"tmsNodeRegistrationStatus": updated.TmsNodeRegistrationStatus,
		"nodeName":                  updated.TmsSourceNodeName,
	})
}

// GetTmsNodeStatus returns the current TMS node registration status.
//
// GET /api/v1/cpiTenant/:id/tms-node/status
func (h *Handler) GetTmsNodeStatus(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var tenant db.CpiTenant
	if err := h.db.First(&tenant, tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	OK(ctx, gin.H{
		"tmsNodeRegistrationStatus": tenant.TmsNodeRegistrationStatus,
		"tmsSourceNodeName":         tenant.TmsSourceNodeName,
	})
}

// GetTmsRoutes fetches the live Route list from TMS for this tenant's source node.
//
// GET /api/v1/cpiTenant/:id/tms-node/routes
//
// Requires TmsNodeRegistrationStatus = registering (node must exist before
// routes can be configured).
func (h *Handler) GetTmsRoutes(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var tenant db.CpiTenant
	if err := h.db.First(&tenant, tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	if tenant.TmsNodeRegistrationStatus != lifecycle.PrereqRegistering {
		Fail(ctx, 400, "routes can only be queried when TmsNodeRegistrationStatus=registering")
		return
	}

	routes, err := h.svc.GetTmsRoutes(ctx.Request.Context(), tenantID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, gin.H{
		"nodeName": tenant.TmsSourceNodeName,
		"routes":   routes,
	})
}

// ConfirmTmsRoutes confirms that the operator has configured Routes in the TMS UI.
//
// POST /api/v1/cpiTenant/:id/tms-node/confirm
//
// Behaviour:
//   - Routes non-empty → TmsNodeRegistrationStatus written to ready → 200
//   - Routes empty     → 400 ROUTES_NOT_CONFIGURED, status stays registering
//
// Requires TmsNodeRegistrationStatus = registering.
func (h *Handler) ConfirmTmsRoutes(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var tenant db.CpiTenant
	if err := h.db.First(&tenant, tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	if tenant.TmsNodeRegistrationStatus != lifecycle.PrereqRegistering {
		Fail(ctx, 409, "confirm requires TmsNodeRegistrationStatus=registering")
		return
	}

	result, err := h.svc.ConfirmTmsRoutes(ctx.Request.Context(), tenantID)
	if err != nil {
		if errors.Is(err, service.ErrRoutesNotConfigured) {
			ctx.JSON(400, gin.H{
				"error":                     "ROUTES_NOT_CONFIGURED",
				"tmsNodeRegistrationStatus": lifecycle.PrereqRegistering,
				"message":                   "No routes found for this source node. Configure a Route in TMS UI and call confirm again.",
			})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, gin.H{
		"tenantId":                  tenantID,
		"tmsNodeRegistrationStatus": lifecycle.PrereqReady,
		"routes":                    result.Routes,
	})
}
