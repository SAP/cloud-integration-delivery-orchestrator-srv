package handler

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
)

// centralTmsContextResponse is the API response for GET /api/v1/centralTmsContext.
// TmsApiEndpoint is derived at read-time from the provider Destination Service and
// is never persisted in the database.
type centralTmsContextResponse struct {
	db.CentralTmsContext
	TmsApiEndpoint string `json:"TmsApiEndpoint,omitempty"`
}

// GetCentralTmsContext returns the current CentralTmsContext configuration.
// In v1 there is exactly one row; returns 404 if none exists yet.
// The response includes TmsApiEndpoint, dynamically resolved from the provider
// Destination Service using TmsApiDestinationName.
func (h *Handler) GetCentralTmsContext(ctx *gin.Context) {
	var tmsCtx db.CentralTmsContext
	if err := h.db.First(&tmsCtx).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, "central TMS context not configured")
		} else {
			Fail(ctx, 500, err.Error())
		}
		return
	}

	resp := centralTmsContextResponse{CentralTmsContext: tmsCtx}
	if tmsCtx.TmsApiDestinationName != "" {
		if dest, err := h.destSvc.GetDestination(ctx.Request.Context(), tmsCtx.TmsApiDestinationName); err == nil && dest != nil {
			resp.TmsApiEndpoint = dest.URL
		}
	}
	OK(ctx, resp)
}

// upsertCentralTmsResponse extends the saved record with the count of tenants
// whose TMS extension was invalidated by Rule 4.
type upsertCentralTmsResponse struct {
	db.CentralTmsContext
	AffectedTenants int64 `json:"affectedTenants"`
}

// UpsertCentralTmsContext creates or fully replaces the CentralTmsContext.
// v1 supports a single record; subsequent PUT calls overwrite it in place.
//
// Rule 4 (RFC 032 §5.4): if TmsApiDestinationName is changed and tenants exist
// with TmsExtensionState=bootstrapped, they are invalidated to bootstrap_failed
// (their subscriber TransportManagementService destination holds stale credentials).
// If any tenant is in TmsExtensionState=bootstrapping, the modification is rejected (409).
func (h *Handler) UpsertCentralTmsContext(ctx *gin.Context) {
	var input db.CentralTmsContext
	if err := ctx.ShouldBindJSON(&input); err != nil {
		Fail(ctx, 400, err.Error())
		return
	}

	var existing db.CentralTmsContext
	err := h.db.First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		Fail(ctx, 500, err.Error())
		return
	}

	// If a record already exists, carry its ID so Save does UPDATE instead of INSERT.
	if existing.ID != 0 {
		input.ID = existing.ID
	}

	// Rule 4: check if TmsApiDestinationName actually changed.
	destNameChanged := existing.ID != 0 && existing.TmsApiDestinationName != input.TmsApiDestinationName

	var affectedCount int64
	if destNameChanged {
		// Rule 4 requires atomicity: reject if bootstrapping, invalidate bootstrapped, save context.
		if err := h.db.Transaction(func(tx *gorm.DB) error {
			// Reject if any tenant is currently bootstrapping TMS extension.
			var bootstrappingCount int64
			tx.Model(&db.CpiTenant{}).
				Where("tms_extension_state = ?", lifecycle.TmsBootstrapping).
				Count(&bootstrappingCount)
			if bootstrappingCount > 0 {
				return fmt.Errorf("TMS_BOOTSTRAP_IN_PROGRESS: %d tenant(s) have TMS extension bootstrap running; wait for completion before changing Central TMS destination", bootstrappingCount)
			}

			// Invalidate all bootstrapped tenants.
			// Also reset TransportManagementDestStatus because the subscriber destination
			// holds credentials derived from the old Central TMS destination.
			result := tx.Model(&db.CpiTenant{}).
				Where("tms_extension_state = ?", lifecycle.TmsBootstrapped).
				Updates(map[string]any{
					"tms_extension_state":              lifecycle.TmsBootstrapFailed,
					"tms_blocking_reason":              lifecycle.TmsReasonCentralTmsChanged,
					"transport_management_dest_status": lifecycle.PrereqMissing,
				})
			if result.Error != nil {
				return fmt.Errorf("rule 4: invalidate tenants: %w", result.Error)
			}
			affectedCount = result.RowsAffected

			return tx.Save(&input).Error
		}); err != nil {
			if strings.Contains(err.Error(), "TMS_BOOTSTRAP_IN_PROGRESS") {
				FailCode(ctx, 409, "TMS_BOOTSTRAP_IN_PROGRESS", err.Error())
			} else {
				Fail(ctx, 500, err.Error())
			}
			return
		}
	} else {
		if err := h.db.Save(&input).Error; err != nil {
			Fail(ctx, 500, err.Error())
			return
		}
	}
	OK(ctx, upsertCentralTmsResponse{
		CentralTmsContext: input,
		AffectedTenants:   affectedCount,
	})
}
