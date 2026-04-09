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

// keySubaccountFields lists the CpiTenant fields whose change invalidates any
// previous bootstrap inspection.  Modifying any of them forces LifecycleState
// back to DRAFT and clears all prerequisite status caches.
var keySubaccountFields = []string{
	"SubaccountID",
	"Region",
	"CfSpace",
	"IntegrationSuiteEndpoint",
}

// UpsertCpiTenant creates or updates a CpiTenant.
//
// Create semantics (ID == 0):
//   - Sets LifecycleState = DRAFT.
//   - Returns 409 if SubaccountID is already in use by another active tenant.
//
// Update semantics (ID > 0):
//   - If any key subaccount field changed (SubaccountID, Region, CfSpace,
//     IntegrationSuiteEndpoint), resets LifecycleState to DRAFT and clears all
//     prerequisite status fields so stale bootstrap results are not trusted.
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

		// New tenants always start in DRAFT; bootstrap has not run yet.
		input.LifecycleState = lifecycle.TenantDraft

		// Reject duplicate SubaccountID among active (non-deleted) tenants.
		if input.SubaccountID != "" {
			var existing db.CpiTenant
			err := h.db.Where(&db.CpiTenant{SubaccountID: input.SubaccountID}).
				First(&existing).Error
			if err == nil {
				Fail(ctx, 409, fmt.Sprintf("BTP subaccount %q is already registered as a CPI tenant", input.SubaccountID))
				return
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				Fail(ctx, 500, err.Error())
				return
			}
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

	// Reject SubaccountID change to a value already owned by another active tenant.
	if input.SubaccountID != "" && input.SubaccountID != existing.SubaccountID {
		var conflict db.CpiTenant
		err := h.db.Where("subaccount_id = ? AND id != ?", input.SubaccountID, input.ID).
			First(&conflict).Error
		if err == nil {
			Fail(ctx, 409, fmt.Sprintf(
				"BTP subaccount %q is already registered as CPI tenant %q (id=%d)",
				input.SubaccountID, conflict.Name, conflict.ID,
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

	// If any key subaccount field changed, invalidate the bootstrap assessment.
	if keyFieldChanged(existing, input) {
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

// keyFieldChanged returns true if any bootstrap-sensitive field differs between
// the stored tenant and the incoming update.
func keyFieldChanged(existing, input db.CpiTenant) bool {
	return existing.SubaccountID != input.SubaccountID ||
		existing.Region != input.Region ||
		existing.CfSpace != input.CfSpace ||
		existing.IntegrationSuiteEndpoint != input.IntegrationSuiteEndpoint
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

// GetCpiTenants lists all active tenants.
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
