package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// centralTmsContextResponse is the API response for GET /api/v1/centralTmsContext.
// TmsApiEndpoint is derived at read-time from the provider Destination Service and
// is never persisted in the database.
type centralTmsContextResponse struct {
	db.CentralTmsContext
	TmsApiEndpoint string `json:"TmsApiEndpoint,omitempty"`
}

// GetCentralTmsContext returns the current CentralTmsContext configuration.
// In v1 there is exactly one row; returns 404 if none exists yet.
// The response includes TmsApiEndpoint, dynamically resolved from the provider
// Destination Service using TmsApiDestinationName.
func (h *Handler) GetCentralTmsContext(ctx *gin.Context) {
	var tmsCtx db.CentralTmsContext
	if err := h.db.First(&tmsCtx).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "central TMS context not configured")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	resp := centralTmsContextResponse{CentralTmsContext: tmsCtx}
	if tmsCtx.TmsApiDestinationName != "" {
		if dest, err := h.destSvc.GetDestination(ctx.Request.Context(), tmsCtx.TmsApiDestinationName); err == nil && dest != nil {
			resp.TmsApiEndpoint = dest.URL
		}
	}
	OK(ctx, resp)
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

	result, err := h.svc.UpsertCentralTmsContext(input)
	if err != nil {
		if errors.Is(err, service.ErrTmsBootstrapInProgress) {
			FailCode(ctx, 409, "TMS_BOOTSTRAP_IN_PROGRESS", err.Error())
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	OK(ctx, upsertCentralTmsResponse{
		CentralTmsContext: result.Context,
		AffectedTenants:   result.AffectedTenants,
	})
}
