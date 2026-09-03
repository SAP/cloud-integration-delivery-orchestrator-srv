package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
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

// UpsertCentralTmsContext creates or fully replaces the CentralTmsContext.
// v1 supports a single record; subsequent PUT calls overwrite it in place.
func (h *Handler) UpsertCentralTmsContext(ctx *gin.Context) {
	var input db.CentralTmsContext
	if err := ctx.ShouldBindJSON(&input); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}

	// Attempt to load an existing record so we can preserve its ID.
	var existing db.CentralTmsContext
	err := h.db.First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		Fail(ctx, 500, err.Error())
		return
	}

	// If a record already exists, carry its ID so Save does UPDATE instead of INSERT.
	if existing.ID != 0 {
		input.ID = existing.ID
	}

	if err := h.db.Save(&input).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, input)
}
