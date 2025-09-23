package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"mmt-delivery/db"
)

// Create or update (upsert) directly using db model
func UpsertCpiTenant(ctx *gin.Context) {
	var tenant db.CpiTenant
	if err := ctx.ShouldBindJSON(&tenant); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := User(ctx)
	tenant.UpdatedBy = user
	if tenant.ID == 0 {
		tenant.CreatedBy = user
	}
	if err := db.Conn().Save(&tenant).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": tenant})
}

// List all
func GetCpiTenants(ctx *gin.Context) {
	var tenants []db.CpiTenant
	if err := db.Conn().Find(&tenants).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": tenants})
}

// Get by id
func GetCpiTenant(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}

	var tenant db.CpiTenant
	if err := db.Conn().First(&tenant, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": tenant})
}

// Delete by id
func DeleteCpiTenant(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}

	if err := db.Conn().Delete(&db.CpiTenant{}, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": id})
}
