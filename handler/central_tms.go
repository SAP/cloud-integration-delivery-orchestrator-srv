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

// UpsertCentralTmsContext creates or fully replaces the CentralTmsContext.
// v1 supports a single record; subsequent PUT calls overwrite it in place.
//
// Rule 4 (RFC 032 §5.4): if TmsApiDestinationName is changed and tenants exist
// with TmsExtensionState=bootstrapped, they are invalidated to not_started.
// If any tenant is in TmsExtensionState=bootstrapping, the modification is rejected (409).
func (h *Handler) UpsertCentralTmsContext(ctx *gin.Context) {
	var input db.CentralTmsContext
	if err := ctx.ShouldBindJSON(&input); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}

	result, err := h.svc.UpsertCentralTmsContext(ctx.Request.Context(), input)
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
