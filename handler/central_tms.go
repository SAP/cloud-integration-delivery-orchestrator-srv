package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"mmt-delivery/db"
)

// GetCentralTmsContext returns the current CentralTmsContext configuration.
// In v1 there is exactly one row; returns 404 if none exists yet.
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
	OK(ctx, tmsCtx)
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
