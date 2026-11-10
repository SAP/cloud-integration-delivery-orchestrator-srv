package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// GetCentralTmsContext returns the current Central TMS configuration.
// All data is read from the TMS manager's cached snapshot.
func (h *Handler) GetCentralTmsContext(ctx *gin.Context) {
	info, err := h.svc.TMS.RuntimeInfo(ctx.Request.Context())
	if err != nil {
		Fail(ctx, 404, "central TMS context not configured")
		return
	}
	OK(ctx, info)
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
