package handler

import (
	"fmt"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/env"

	"github.com/gin-gonic/gin"
)

// --- Integration Config CRUD ---

// GetIntegrations returns all integration configs.
// GET /api/v1/system/integrations
func (h *Handler) GetIntegrations(ctx *gin.Context) {
	configs, err := db.GetAllIntegrationConfigs(h.db)
	if err != nil {
		Fail(ctx, 500, fmt.Sprintf("failed to get integration configs: %s", err))
		return
	}
	OK(ctx, configs)
}

// UpdateIntegration updates DestinationName, Enabled, and Description for a given integration type.
// PUT /api/v1/system/integrations/:type
func (h *Handler) UpdateIntegration(ctx *gin.Context) {
	integrationType := ctx.Param("type")

	var req struct {
		DestinationName string `json:"destinationName"`
		Enabled         bool   `json:"enabled"`
		Description     string `json:"description"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, 400, fmt.Sprintf("invalid request body: %s", err))
		return
	}

	config, err := db.UpdateIntegrationConfig(h.db, integrationType, req.DestinationName, req.Enabled, req.Description)
	if err != nil {
		Fail(ctx, 500, fmt.Sprintf("failed to update integration '%s': %s", integrationType, err))
		return
	}

	// Invalidate resolver cache for this destination so next access fetches fresh data
	h.destSvc.Invalidate(req.DestinationName)

	OK(ctx, config)
}

// --- System Connectivity Check ---

type ConnectivityStatus struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type ConnectivityReport struct {
	CheckedAt time.Time            `json:"checkedAt"`
	Results   []ConnectivityStatus `json:"results"`
}

// CheckConnectivity verifies connectivity to all external dependencies.
// GET /api/v1/system/connectivity
func (h *Handler) CheckConnectivity(ctx *gin.Context) {
	var results []ConnectivityStatus
	checkCtx := ctx.Request.Context()

	// 1. Database
	sqlDB, err := h.db.DB()
	if err != nil {
		results = append(results, ConnectivityStatus{Name: "Database", Type: "database", Status: "error", Message: err.Error()})
	} else if err := sqlDB.Ping(); err != nil {
		results = append(results, ConnectivityStatus{Name: "Database", Type: "database", Status: "error", Message: err.Error()})
	} else {
		results = append(results, ConnectivityStatus{Name: "Database", Type: "database", Status: "ok"})
	}

	// 2. TMS
	tmsClient, err := h.svc.TmsSvc(checkCtx)
	if err != nil {
		results = append(results, ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()})
	} else if _, err = tmsClient.GetNodes(checkCtx); err != nil {
		results = append(results, ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()})
	} else {
		results = append(results, ConnectivityStatus{Name: "TMS", Type: "tms", Status: "ok"})
		// Update LastValidatedAt on successful TMS connectivity check.
		now := time.Now()
		h.db.Model(&db.CentralTmsContext{}).Where("1 = 1").Update("last_validated_at", now)
	}

	// 3. CPI Tenants — resolve destination then verify OAuth2 token can be obtained
	var tenants []db.CpiTenant
	h.db.Find(&tenants)
	for _, t := range tenants {
		destName := t.PirApiDestinationName
		if destName == "" {
			results = append(results, ConnectivityStatus{
				Name: t.Name, Type: "cpi_tenant", Status: "error",
				Message: "no CPI destination configured (PirApiDestinationName is empty — bootstrap required)",
			})
			continue
		}
		dest, err := h.destSvc.GetDestination(checkCtx, destName)
		if err != nil {
			results = append(results, ConnectivityStatus{
				Name: t.Name, Type: "cpi_tenant", Status: "error",
				Message: fmt.Sprintf("destination '%s' not found: %s", destName, err),
			})
			continue
		}
		// Verify credentials by attempting OAuth2 token acquisition
		if _, err := env.NewClient(checkCtx, dest.ClientId, dest.ClientSecret, dest.TokenServiceURL, dest.URL); err != nil {
			results = append(results, ConnectivityStatus{
				Name: t.Name, Type: "cpi_tenant", Status: "error",
				Message: fmt.Sprintf("destination '%s' found but token fetch failed: %s", destName, err),
			})
		} else {
			results = append(results, ConnectivityStatus{
				Name: t.Name, Type: "cpi_tenant", Status: "ok",
				Message: fmt.Sprintf("destination '%s' resolved and authenticated", destName),
			})
		}
	}

	// 4. Singleton integrations — check enabled ones
	configs, _ := db.GetAllIntegrationConfigs(h.db)
	for _, cfg := range configs {
		if !cfg.Enabled {
			results = append(results, ConnectivityStatus{
				Name: cfg.Type, Type: "integration", Status: "disabled",
			})
			continue
		}
		_, err := h.destSvc.GetDestination(checkCtx, cfg.DestinationName)
		if err != nil {
			results = append(results, ConnectivityStatus{
				Name: cfg.Type, Type: "integration", Status: "error",
				Message: fmt.Sprintf("destination '%s': %s", cfg.DestinationName, err),
			})
		} else {
			results = append(results, ConnectivityStatus{
				Name: cfg.Type, Type: "integration", Status: "ok",
				Message: fmt.Sprintf("destination '%s' resolved", cfg.DestinationName),
			})
		}
	}

	OK(ctx, ConnectivityReport{
		CheckedAt: time.Now(),
		Results:   results,
	})
}
