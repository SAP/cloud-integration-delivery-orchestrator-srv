package handler

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/service"
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

		input.CreatedBy = user

		// CfApiEndpoint and CfOrg are required: without them the tenant cannot enter
		// any meaningful bootstrap flow, and the DB unique index cannot protect
		// against multiple empty-string rows at the business-logic level.
		if input.CfApiEndpoint == "" || input.CfOrg == "" {
			Fail(ctx, 400, "cfApiEndpoint and cfOrg are required")
			return
		}

		// New tenants always start in DRAFT; bootstrap has not run yet.
		input.LifecycleState = lifecycle.TenantDraft

		// Reject duplicate (CfApiEndpoint, CfOrg) pair among active (non-deleted) tenants.
		var existing db.CpiTenant
		err := h.db.Where("cf_api_endpoint = ? AND cf_org = ?", input.CfApiEndpoint, input.CfOrg).
			First(&existing).Error
		if err == nil {
			Fail(ctx, 409, fmt.Sprintf("CF org %q on %q is already registered as a CPI tenant", input.CfOrg, input.CfApiEndpoint))
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 500, err.Error())
			return
		}

		if err := h.db.Create(&input).Error; err != nil {
			Fail(ctx, 500, err.Error())
			return
		}
		OK(ctx, input)
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

	// Reject CfOrg change to a value already owned by another active tenant on the same endpoint.
	if input.CfOrg != "" && (input.CfOrg != existing.CfOrg || input.CfApiEndpoint != existing.CfApiEndpoint) {
		var conflict db.CpiTenant
		err := h.db.Where("cf_api_endpoint = ? AND cf_org = ? AND id != ?", input.CfApiEndpoint, input.CfOrg, input.ID).
			First(&conflict).Error
		if err == nil {
			Fail(ctx, 409, fmt.Sprintf(
				"CF org %q on %q is already registered as CPI tenant %q (id=%d)",
				input.CfOrg, input.CfApiEndpoint, conflict.Name, conflict.ID,
			))
			return
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 500, err.Error())
			return
		}
	}

	// Callers must not set LifecycleState directly; preserve the current value.
	input.LifecycleState = existing.LifecycleState

	// If any key subaccount field changed, transition through the state machine.
	// TransitionLifecycle rejects the event when bootstrap is in progress
	// (TenantReadying has no EventKeyFieldChanged edge), returning 409.
	if keyFieldChanged(existing, input) {
		if err := h.svc.TransitionLifecycle(input.ID, service.EventKeyFieldChanged); err != nil {
			if errors.Is(err, service.ErrTransitionNotAllowed) {
				Fail(ctx, 409, "bootstrap is in progress; key fields cannot be modified")
				return
			}
			Fail(ctx, 500, err.Error())
			return
		}
		input.LifecycleState = lifecycle.TenantDraft
		input.BlockingReason = ""
		clearPrerequisiteStatuses(&input)
	}

	if err := h.db.Save(&input).Error; err != nil {
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, input)
}

// keyFieldChanged returns true if any bootstrap-sensitive CF identity field
// differs between the stored tenant and the incoming update.
// The three key fields are: CfApiEndpoint, CfOrg, CfSpace.
// Changing any of them invalidates previous bootstrap results and forces
// LifecycleState back to DRAFT via TransitionLifecycle(EventKeyFieldChanged).
func keyFieldChanged(existing, input db.CpiTenant) bool {
	return existing.CfApiEndpoint != input.CfApiEndpoint ||
		existing.CfOrg != input.CfOrg ||
		existing.CfSpace != input.CfSpace
}

// clearPrerequisiteStatuses resets all local prerequisite status fields to
// "missing" so the UI shows an accurate "not yet inspected" state after a key
// field change.
func clearPrerequisiteStatuses(t *db.CpiTenant) {
	t.PirApiStatus = lifecycle.PrereqMissing
	t.CasApplicationStatus = lifecycle.PrereqMissing
	t.CasStandardStatus = lifecycle.PrereqMissing
	t.CloudIntegrationDestStatus = lifecycle.PrereqMissing
	t.ContentAssemblyDestStatus = lifecycle.PrereqMissing
	t.TransportManagementDestStatus = lifecycle.PrereqMissing
	t.TmsNodeRegistrationStatus = lifecycle.PrereqMissing
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
