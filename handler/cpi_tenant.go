package handler

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"
)

// UpsertCpiTenant creates or updates a CpiTenant.
//
// Create semantics (ID == 0):
//   - Sets LifecycleState = DRAFT.
//   - Returns 409 if (CfApiEndpoint, CfOrg) pair is already in use by another active tenant.
//
// Update semantics (ID > 0):
//   - If any key CF identity field changed (CfApiEndpoint, CfOrg, CfSpace),
//     resets LifecycleState to DRAFT and clears all prerequisite status fields
//     so stale bootstrap results are not trusted.
//   - LifecycleState is never writable by callers; it is managed by the service layer.
func (h *Handler) UpsertCpiTenant(ctx *gin.Context) {
	var input db.CpiTenant
	if err := ctx.ShouldBindJSON(&input); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}

	user := service.UserID(ctx)
	input.UpdatedBy = user

	if input.ID == 0 {
		// ── Create path ──────────────────────────────────────────────────────────
		// Creates a placeholder record with Name and Group only.
		// CF identity (CfApiEndpoint, CfOrg, CfSpace) is set later via
		// PUT /api/v1/cpiTenant/:id/cfIdentity when the operator starts bootstrap.
		if input.Name == "" {
			Fail(ctx, 400, "name is required")
			return
		}

		placeholder := db.CpiTenant{
			Name:           input.Name,
			Group:          input.Group,
			CreatedBy:      user,
			UpdatedBy:      user,
			LifecycleState: lifecycle.TenantDraft,
		}
		if err := h.db.Create(&placeholder).Error; err != nil {
			if isUniqueViolation(err) {
				FailCode(ctx, 409, "TENANT_NAME_EXISTS", fmt.Sprintf("tenant name %q already exists", input.Name))
				return
			}
			Fail(ctx, 500, err.Error())
			return
		}
		OK(ctx, placeholder)
		return
	}

	// ── Update path ──────────────────────────────────────────────────────────────

	var existing db.CpiTenant
	if err := h.db.First(&existing, input.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	// Whitelist update: only allow operator-editable fields.
	// All other fields (CF identity, lifecycle, TMS extension, prerequisites,
	// destinations) are managed exclusively by their owning service modules
	// and are never written here — no guard needed.
	if err := h.db.Model(&existing).Updates(map[string]any{
		"name":       input.Name,
		"group":      input.Group,
		"updated_by": input.UpdatedBy,
	}).Error; err != nil {
		if isUniqueViolation(err) {
			FailCode(ctx, 409, "TENANT_NAME_EXISTS", fmt.Sprintf("tenant name %q already exists", input.Name))
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}
	// Re-read the full record to return current state.
	if err := h.db.First(&existing, input.ID).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, existing)
}

// SaveCfIdentity persists the CF identity fields (CfApiEndpoint, CfOrg, CfSpace)
// for the tenant and validates the operator's cfToken against the CF API.
//
// This is the terminal action of Wizard Step 1.  On success the tenant
// transitions to LifecycleState = CONFIGURED and the caller can proceed to
// Wizard Steps 2–3 (Inspect + Apply) within the same cfToken session.
//
// Requires: { "cfApiEndpoint", "cfOrg", "cfSpace", "cfToken" } in the request body.
// cfToken is never persisted — it is used only for the in-request CF validation.
//
// PUT /api/v1/cpiTenant/:id/cfIdentity
func (h *Handler) SaveCfIdentity(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var body struct {
		service.CfIdentityInput
		CfToken string `json:"cfToken" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}

	if err := h.svc.SaveCfIdentity(ctx.Request.Context(), tenantID, body.CfIdentityInput, body.CfToken); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "tenant not found")
			return
		}
		Fail(ctx, 500, err.Error())
		return
	}

	OK(ctx, gin.H{"tenantId": tenantID})
}


func (h *Handler) GetCpiTenants(ctx *gin.Context) {
	var tenants []db.CpiTenant
	if err := h.db.Find(&tenants).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, tenants)
}

// GetCpiTenant returns a single tenant by ID.
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

// DeleteCpiTenant soft-deletes a tenant by ID.
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
