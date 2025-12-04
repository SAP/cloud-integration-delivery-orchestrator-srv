package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

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

	// Use explicit association updates for deterministic many2many behavior
	if err := db.Conn().Transaction(func(tx *gorm.DB) error {
		// Recompute based on incoming IncludedTenants
		sourceTenant, targetRoutes, targetNodes, err := service.SourceAndRoute(rule.IncludedTenants)
		if err != nil {
			return fmt.Errorf("failed to determine source tenant and target routes/nodes: %s", err.Error())
		}
		rule.SourceTenantID = sourceTenant.ID
		rule.TargetNodes, rule.TargetRoutes = targetNodes, targetRoutes

		if rule.ID == 0 {
			// Create scalar fields first
			if err := tx.Create(&rule).Error; err != nil {
				return err
			}
			// Replace associations explicitly
			if err := tx.Model(&rule).Association("IncludedTenants").Replace(rule.IncludedTenants); err != nil {
				return err
			}
			if err := tx.Model(&rule).Association("ExcludedTenants").Replace(rule.ExcludedTenants); err != nil {
				return err
			}
			return nil
		}

		// Update path: load existing then update scalars
		var existing db.DeliveryRule
		if err := tx.First(&existing, rule.ID).Error; err != nil {
			return err
		}

		existing.Name = rule.Name
		existing.VersionPattern = rule.VersionPattern
		existing.SourceTenantID = rule.SourceTenantID
		existing.TargetNodes = rule.TargetNodes
		existing.TargetRoutes = rule.TargetRoutes
		existing.SkipApprove = rule.SkipApprove
		existing.Active = rule.Active
		existing.UpdatedBy = user

		if err := tx.Save(&existing).Error; err != nil {
			return err
		}

		// Replace associations to reflect the incoming payload exactly
		if err := tx.Model(&existing).Association("IncludedTenants").Replace(rule.IncludedTenants); err != nil {
			return err
		}
		if err := tx.Model(&existing).Association("ExcludedTenants").Replace(rule.ExcludedTenants); err != nil {
			return err
		}

		rule = existing
		return nil
	}); err != nil {
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
