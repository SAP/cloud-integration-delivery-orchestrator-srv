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
	if err := db.Conn().Save(&rule).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	if err := service.GenRouteForRule(rule.ID); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Errorf("failed to generate delivery route by included tenants: %s", err.Error())})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": rule})
}

// List all
func GetDeliveryRules(ctx *gin.Context) {
	var rules []db.DeliveryRule
	if err := db.Conn().Find(&rules).Error; err != nil {
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
	var rule db.DeliveryRule
	if err := db.Conn().First(&rule, id).Error; err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"status": "fail", "code": 404, "error": "not found"})
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
