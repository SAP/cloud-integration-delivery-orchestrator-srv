package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"mmt-delivery/pkg/errcode"
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
