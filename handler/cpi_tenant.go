package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"mmt-delivery/db"
	"mmt-delivery/service"
)

// Create or update (upsert) directly using db model
func (h *Handler) UpsertCpiTenant(ctx *gin.Context) {
	var tenant db.CpiTenant
	if err := ctx.ShouldBindJSON(&tenant); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}
	user := service.UserID(ctx)
	tenant.UpdatedBy = user
	if tenant.ID == 0 {
		tenant.CreatedBy = user
	}
	if err := h.db.Save(&tenant).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, tenant)
}

// List all
func (h *Handler) GetCpiTenants(ctx *gin.Context) {
	var tenants []db.CpiTenant
	if err := h.db.Find(&tenants).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, tenants)
}

// Get by id
func (h *Handler) GetCpiTenant(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		Fail(ctx, 400, "invalid id")
		return
	}

	var tenant db.CpiTenant
	if err := h.db.First(&tenant, id).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, tenant)
}

// Delete by id
func (h *Handler) DeleteCpiTenant(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		Fail(ctx, 400, "invalid id")
		return
	}

	if err := h.db.Delete(&db.CpiTenant{}, id).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, id)
}

func (h *Handler) CpiTenantCounts(ctx *gin.Context) {
	var res StatusCount
	if err := h.db.Model(&db.CpiTenant{}).Count(&res.Total).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, res)
}
