package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/tms"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// GetCentralTmsContext returns the current Central TMS configuration from the
// TMS manager's cached snapshot. If the client has not loaded yet (cold start)
// and the first load fails (e.g. BTP destination temporarily unavailable), the
// config still EXISTS — report it from the DB row with Loaded=false rather than
// a misleading 404 "not configured" that would send operators down the wrong
// path (R3). A true 404 is only for when no config row exists at all.
func (h *Handler) GetCentralTmsContext(ctx *gin.Context) {
	info, err := h.svc.TMS.RuntimeInfo(ctx.Request.Context())
	if err == nil {
		OK(ctx, info)
		return
	}

	// RuntimeInfo failed — distinguish "never configured" from "configured but
	// not loaded yet" by consulting the persisted config.
	var cfg db.CentralTmsContext
	if dbErr := h.db.First(&cfg).Error; dbErr != nil {
		if errors.Is(dbErr, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "central TMS context not configured")
		} else {
			Fail(ctx, 500, dbErr.Error())
		}
		return
	}
	OK(ctx, tms.RuntimeInfo{
		Loaded:          false,
		DestinationName: cfg.TmsApiDestinationName,
		LastValidatedAt: cfg.LastValidatedAt,
	})
}

// upsertCentralTmsResponse extends the saved record with the count of tenants
// whose TMS extension was invalidated by Rule 4.
type upsertCentralTmsResponse struct {
	db.CentralTmsContext
	AffectedTenants int64 `json:"affectedTenants"`
}

// UpsertCentralTmsContext creates or re-applies the Central TMS configuration.
// The request carries only the destination name; every save is a full
// reconfigure that re-reads/validates the destination and runs Rule 4 (reset
// affected tenants), even when the name is unchanged. A concurrent reconfigure
// yields 409 TMS_CONFIG_CHANGED; a bootstrapping tenant yields 409.
func (h *Handler) UpsertCentralTmsContext(ctx *gin.Context) {
	var req struct {
		TmsApiDestinationName string `json:"TmsApiDestinationName"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}

	result, err := h.svc.UpsertCentralTmsContext(ctx.Request.Context(), req.TmsApiDestinationName)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTmsBootstrapInProgress):
			FailCode(ctx, 409, "TMS_BOOTSTRAP_IN_PROGRESS", err.Error())
		case errors.Is(err, service.ErrTmsConfigConflict):
			FailCode(ctx, 409, "TMS_CONFIG_CHANGED", err.Error())
		default:
			Fail(ctx, 500, err.Error())
		}
		return
	}

	OK(ctx, upsertCentralTmsResponse{
		CentralTmsContext: result.Context,
		AffectedTenants:   result.AffectedTenants,
	})
}
