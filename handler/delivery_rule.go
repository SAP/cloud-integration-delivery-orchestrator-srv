package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"mmt-delivery/db"
	"mmt-delivery/service"
)

// Create or update (upsert)
func UpsertDeliveryRule(ctx *gin.Context) {
	var rule db.DeliveryRule
	if err := ctx.ShouldBindJSON(&rule); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := service.UserID(ctx)
	rule.UpdatedBy = user
	if rule.ID == 0 {
		rule.CreatedBy = user
	}
	// determine source tenant and target routes/nodes based on included tenants
	sourceTenant, targetRoutes, targetNodes, err := service.SourceAndRoute(rule.IncludedTenants)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Errorf("failed to determine source tenant and target routes/nodes: %s", err.Error())})
		return
	}
	rule.SourceTenantID = sourceTenant.ID
	rule.TargetNodes, rule.TargetRoutes = targetNodes, targetRoutes
	if err := db.Conn().Save(&rule).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": rule})
}

// List all
func GetDeliveryRules(ctx *gin.Context) {
	var rules []db.DeliveryRule
	if err := db.Conn().
		Preload("IncludedTenants").
		Preload("ExcludedTenants").
		Find(&rules).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": rules})
}

// Get by id
func GetDeliveryRule(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}
	rule, err := service.GetDeliveryRule(uint(id))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": rule})
}

// Delete by id
func DeleteDeliveryRule(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}
	if err := db.Conn().Delete(&db.DeliveryRule{}, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": id})
}
