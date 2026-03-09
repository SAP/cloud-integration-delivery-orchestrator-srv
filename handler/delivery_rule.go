package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"mmt-delivery/db"
	"mmt-delivery/service"
)

func RuleCheck(c *gin.Context) {
	var req OpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if req.DeliveryRequestID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing deliveryRequestID"})
		return
	}
	dr, err := service.QueryDrWithAssociations(req.DeliveryRequestID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	rule, err := service.GetDeliveryRuleWithAcc(dr.DeliveryRuleID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	for i := range req.Ops {
		op := &req.Ops[i]
		if err := service.DeliveryRuleCheck(op, &rule); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": "delivery rule check passed"})
}

// Create or update (upsert)
func UpsertDeliveryRule(ctx *gin.Context) {
	var rule db.DeliveryRule
	if err := ctx.ShouldBindJSON(&rule); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if rule.Name == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "rule name cannot be empty"})
		return
	}
	if rule.VersionPattern == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "VersionPattern cannot be empty"})
		return
	}
	// Validate VersionPattern: allow formats like 5.8.* , 6.2.*
	vpRe := regexp.MustCompile(`^\d+\.\d+\.\*$`)
	if !vpRe.MatchString(rule.VersionPattern) {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid VersionPattern: expected format X.Y.* (e.g., 5.8.*, 6.2.*)"})
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
		sourceTenant, targetRoutes, targetNodes, err := service.SourceAndRoute(ctx, rule.IncludedTenants)
		if err != nil {
			return fmt.Errorf("failed to determine source tenant and target routes/nodes: %s", err.Error())
		}
		rule.SourceTenantID = sourceTenant.ID
		rule.TargetNodes, rule.TargetRoutes = targetNodes, targetRoutes

		if err := tx.Save(&rule).Error; err != nil {
			return err
		}
		// Replace associations to reflect the incoming payload exactly
		if err := tx.Model(&rule).Association("IncludedTenants").Replace(rule.IncludedTenants); err != nil {
			return err
		}
		if err := tx.Model(&rule).Association("ExcludedTenants").Replace(rule.ExcludedTenants); err != nil {
			return err
		}
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
	rule, err := service.GetDeliveryRuleWithAcc(uint(id))
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
