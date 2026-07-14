package handler

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"mmt-delivery/db"
	"mmt-delivery/service"
)

func (h *Handler) RuleCheck(c *gin.Context) {
	var req OpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		Fail(c, 400, err.Error())
		return
	}
	if req.DeliveryRequestID == 0 {
		Fail(c, 400, "missing deliveryRequestID")
		return
	}
	dr, err := h.svc.QueryDrWithAssociations(req.DeliveryRequestID)
	if err != nil {
		Fail(c, 500, err.Error())
		return
	}
	rule, err := h.svc.GetDeliveryRuleWithAcc(dr.DeliveryRuleID)
	if err != nil {
		Fail(c, 500, err.Error())
		return
	}
	for i := range req.Ops {
		op := &req.Ops[i]
		if err := h.svc.DeliveryRuleCheck(c.Request.Context(), op, &rule); err != nil {
			Fail(c, 400, err.Error())
			return
		}
	}
	OK(c, "delivery rule check passed")
}

// Create or update (upsert)
func (h *Handler) UpsertDeliveryRule(ctx *gin.Context) {
	var rule db.DeliveryRule
	if err := ctx.ShouldBindJSON(&rule); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}
	if rule.Name == "" {
		Fail(ctx, 400, "rule name cannot be empty")
		return
	}
	if rule.VersionPattern == "" {
		Fail(ctx, 400, "VersionPattern cannot be empty")
		return
	}
	// Validate VersionPattern: allow formats like 5.8.* , 6.2.*
	vpRe := regexp.MustCompile(`^\d+\.\d+\.\*$`)
	if !vpRe.MatchString(rule.VersionPattern) {
		Fail(ctx, 400, "invalid VersionPattern: expected format X.Y.* (e.g., 5.8.*, 6.2.*)")
		return
	}

	user := service.UserID(ctx)
	rule.UpdatedBy = user
	if rule.ID == 0 {
		rule.CreatedBy = user
	}

	// Use explicit association updates for deterministic many2many behavior
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		// Recompute based on incoming IncludedTenants
		sourceTenant, targetRoutes, targetNodes, err := h.svc.SourceAndRoute(ctx, rule.IncludedTenants)
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
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, rule)
}

// List all
func (h *Handler) GetDeliveryRules(ctx *gin.Context) {
	var rules []db.DeliveryRule
	if err := h.db.
		Preload("IncludedTenants").
		Preload("ExcludedTenants").
		Find(&rules).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, rules)
}

// Get by id
func (h *Handler) GetDeliveryRule(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		Fail(ctx, 400, "invalid id")
		return
	}
	rule, err := h.svc.GetDeliveryRuleWithAcc(uint(id))
	if err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, rule)
}

// Delete by id
func (h *Handler) DeleteDeliveryRule(ctx *gin.Context) {
	idStr := ctx.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		Fail(ctx, 400, "invalid id")
		return
	}
	if err := h.db.Delete(&db.DeliveryRule{}, id).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, id)
}

func (h *Handler) DeliveryRuleCounts(ctx *gin.Context) {
	var res StatusCount
	if err := h.db.Model(&db.DeliveryRule{}).Count(&res.Total).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, res)
}
