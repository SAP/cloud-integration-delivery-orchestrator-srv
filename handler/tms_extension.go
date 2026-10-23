package handler

// tms_extension.go — TMS extension HTTP endpoints (RFC 032 P0).
//
// These endpoints replace the old /tms-node/* endpoints and add bootstrap
// capabilities for the TMS extension layer.
//
// Routes (registered in handler.go under CpiTenant.Manage scope):
//
//	GET   /api/v1/cpiTenant/:id/tms-extension          — status overview
//	POST  /api/v1/cpiTenant/:id/tms-extension/node     — select TMS node
//	GET   /api/v1/cpiTenant/:id/tms-extension/routes   — live route list
//	POST  /api/v1/cpiTenant/:id/tms-extension/confirm  — confirm routes (P2b will remove)
//	POST  /api/v1/cpiTenant/:id/tms-extension/preview  — read-only inspect
//	POST  /api/v1/cpiTenant/:id/tms-extension/apply    — bootstrap apply/retry
//	POST  /api/v1/cpiTenant/:id/tms-extension/reset    — escape hatch

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// GetTmsExtension returns the TMS extension status overview for a tenant.
//
// GET /api/v1/cpiTenant/:id/tms-extension
func (h *Handler) GetTmsExtension(ctx *gin.Context) {
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

	// Compute availability (not stored, checked dynamically).
	available := true
	var availabilityReason string
	if !tenant.IsOnboarded() {
		available = false
		availabilityReason = lifecycle.TmsReasonBaseNotReady
	} else {
		tmsCtx, err := h.svc.LoadCentralTmsContext()
		if err != nil {
			available = false
			availabilityReason = lifecycle.TmsReasonCentralTmsNotConfigured
		}
		_ = tmsCtx
	}

	// Latest TMS job.
	var latestJob *db.TenantBootstrapJob
	var job db.TenantBootstrapJob
	if err := h.db.Where("cpi_tenant_id = ? AND layer = ?", tenantID, lifecycle.LayerTMS).
		Order("created_at DESC").First(&job).Error; err == nil {
		latestJob = &job
	}

	OK(ctx, gin.H{
		"tmsExtensionState": tenant.TmsExtensionState,
		"tmsBlockingReason": tenant.TmsBlockingReason,
		"available":         available,
		"availabilityReason": availabilityReason,
		"nodeName":          tenant.TmsSourceNodeName,
		"nodeId":            tenant.TmsSourceNodeID,
		"casApplicationStatus":          tenant.CasApplicationStatus,
		"casStandardStatus":             tenant.CasStandardStatus,
		"cloudIntegrationDestStatus":    tenant.CloudIntegrationDestStatus,
		"contentAssemblyDestStatus":     tenant.ContentAssemblyDestStatus,
		"transportManagementDestStatus": tenant.TransportManagementDestStatus,
		"latestJob":                     latestJob,
	})
}

// SelectTmsNode sets the TMS source node for the tenant.
//
// POST /api/v1/cpiTenant/:id/tms-extension/node
func (h *Handler) SelectTmsNode(ctx *gin.Context) {
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

	var tenant db.CpiTenant
	if err := h.db.First(&tenant, tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	if tenant.LifecycleState == lifecycle.TenantDraft {
		Fail(ctx, 400, "complete CF Connection before selecting a TMS node")
		return
	}

	if err := h.svc.SelectTmsNode(ctx.Request.Context(), tenantID, body.NodeId, body.NodeName); err != nil {
		Fail(ctx, 409, err.Error())
		return
	}

	OK(ctx, gin.H{
		"tmsExtensionState": lifecycle.TmsNodePending,
		"nodeName":          body.NodeName,
		"nodeId":            body.NodeId,
	})
}

// GetTmsExtensionRoutes fetches the live Route list from TMS for the tenant's node.
//
// GET /api/v1/cpiTenant/:id/tms-extension/routes
func (h *Handler) GetTmsExtensionRoutes(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	result, err := h.svc.GetCurrentNodeRoutes(ctx.Request.Context(), tenantID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			Fail(ctx, 404, "tenant not found")
		case errors.Is(err, service.ErrNodeNotRegistered):
			Fail(ctx, 400, err.Error())
		default:
			Fail(ctx, 500, err.Error())
		}
		return
	}

	OK(ctx, gin.H{
		"nodeName": result.NodeName,
		"routes":   result.Routes,
	})
}

// ConfirmTmsExtensionRoutes confirms operator has configured routes in TMS UI.
//
// POST /api/v1/cpiTenant/:id/tms-extension/confirm
func (h *Handler) ConfirmTmsExtensionRoutes(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	result, err := h.svc.ConfirmTmsRoutesAndTransition(ctx.Request.Context(), tenantID)
	if err != nil {
		if errors.Is(err, service.ErrRoutesNotConfigured) {
			ctx.JSON(400, gin.H{
				"error":   "ROUTES_NOT_CONFIGURED",
				"message": "No routes found for this source node. Configure a Route in TMS UI and call confirm again.",
			})
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, gin.H{
		"tmsExtensionState": lifecycle.TmsNodeConfirmed,
		"routes":            result.Routes,
	})
}

// PreviewTmsExtension runs a read-only inspection of TMS extension prerequisites.
//
// POST /api/v1/cpiTenant/:id/tms-extension/preview
func (h *Handler) PreviewTmsExtension(ctx *gin.Context) {
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

	preview, err := h.svc.PreviewTmsExtension(ctx.Request.Context(), tenantID, body.CfToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	OK(ctx, preview)
}

// ApplyTmsExtension creates a TMS extension bootstrap job.
//
// POST /api/v1/cpiTenant/:id/tms-extension/apply
func (h *Handler) ApplyTmsExtension(ctx *gin.Context) {
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

	jobID, err := h.svc.ApplyTmsExtension(ctx.Request.Context(), tenantID, body.CfToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	OK(ctx, gin.H{"jobId": jobID})
}

// ResetTmsExtension is the escape hatch for tenants stuck in bootstrapping state.
//
// POST /api/v1/cpiTenant/:id/tms-extension/reset
func (h *Handler) ResetTmsExtension(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	if err := h.svc.ResetTmsExtension(tenantID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 409, err.Error())
		}
		return
	}

	OK(ctx, gin.H{"tenantId": tenantID})
}
