package service

// tms_node_registrar.go — TMS Node registration independent lifecycle (Phase 3).
//
// TMS Node registration is a synchronous HTTP operation separate from the
// bootstrap apply/retry lifecycle.  It is triggered by the operator after
// bootstrap completes (LifecycleState = ready) via the /tms-node/* endpoints.
//
// Registration mode:
//   - manual: operator pre-creates the TMS node; product validates its structure
//             (node existence + at least one target with a non-empty DestinationName).
//
// mode=auto is not supported in the current deployment topology; see ErrAutoModeNotSupported.
//
// After registration (TmsNodeRegistrationStatus = registering), the operator
// configures a Route in TMS UI and calls POST /tms-node/confirm to transition
// to ready.  If the Route list is empty at confirm time, a 400 error is
// returned and the status stays at registering.
//
// Design reference: 02-bootstrap-state-and-data-model.md § "TMS Node 注册独立生命周期"
//                   05-detailed-execution-tasks.md § 3.1–3.3

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
)

// ── Public errors ─────────────────────────────────────────────────────────────

// ErrRoutesNotConfigured is returned by ConfirmTmsRoutes when the operator
// calls confirm but the Route list fetched from TMS is empty.  The caller
// should translate this to a 400 HTTP response with error ROUTES_NOT_CONFIGURED.
var ErrRoutesNotConfigured = fmt.Errorf("routes not configured: no routes found for this source node")

// ErrAlreadyRegistering is returned when RegisterTmsNode detects that
// TmsNodeRegistrationStatus is already registering inside its atomic
// SELECT FOR UPDATE guard, preventing a double-registration race.
var ErrAlreadyRegistering = fmt.Errorf("TMS node is already in registering state; complete Route configuration and call confirm, or wait before re-registering")

// ErrAutoModeNotSupported is returned when mode=auto is requested.
// Auto mode requires write access to the TMS subaccount's Destination Service,
// which is not available in the current deployment topology (cpi-delivery's
// ProviderDest and the TMS subaccount Destination Service are separate instances
// in a multi-subaccount BTP setup).  The feature may be re-introduced in a
// future SaaS phase with cross-subaccount Destination Service access.
var ErrAutoModeNotSupported = fmt.Errorf("auto mode not supported: registerAuto requires write access to the TMS subaccount Destination Service, which is unavailable in the current topology; use mode=manual")

// ── ConfirmTmsRoutesResult ────────────────────────────────────────────────────

// ConfirmTmsRoutesResult holds the routes returned by a successful confirm call.
type ConfirmTmsRoutesResult struct {
	Routes []tms.V1TransportRoute
}

// ── RegisterTmsNode ───────────────────────────────────────────────────────────

// RegisterTmsNode executes TMS Node registration for the given tenant.
//
// Only mode="manual" is supported.  mode="auto" is not implemented in the
// current deployment topology (see ErrAutoModeNotSupported).
// nodeName is required for manual mode.
//
// Atomically claims TmsNodeRegistrationStatus = registering via SELECT FOR UPDATE
// before executing any TMS API calls, preventing double-registration races.
// Returns ErrAlreadyRegistering if another request already claimed the slot.
//
// On success TmsNodeRegistrationStatus is written to registering and TmsSourceNodeName
// is set (or updated if different from the stored value).
// On failure TmsNodeRegistrationStatus is written to failed.
func (s *Service) RegisterTmsNode(ctx context.Context, tenantID uint, mode, nodeName string) error {
	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return fmt.Errorf("RegisterTmsNode: get central TMS context: %w", err)
	}

	nodeClient, err := buildTmsClient(ctx, tmsCtx, s.ProviderDest)
	if err != nil {
		return fmt.Errorf("RegisterTmsNode: build TMS node client: %w", err)
	}

	// Atomically claim registering status to prevent TOCTOU double-registration.
	// The transaction holds a row-level lock until it commits, so a concurrent
	// request that also passed the handler guard will block here and then see
	// PrereqRegistering, returning ErrAlreadyRegistering.
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t db.CpiTenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "tms_node_registration_status").
			First(&t, tenantID).Error; err != nil {
			return err
		}
		if t.TmsNodeRegistrationStatus == lifecycle.PrereqRegistering {
			return ErrAlreadyRegistering
		}
		updates := map[string]any{
			"tms_node_registration_status": lifecycle.PrereqRegistering,
		}
		if nodeName != "" {
			updates["tms_source_node_name"] = nodeName
		}
		return tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(updates).Error
	}); err != nil {
		return err
	}

	// registering claimed — now perform mode-specific TMS API validation.
	// Any failure here writes PrereqFailed (best-effort) and returns the error.
	switch mode {
	case "manual":
		return s.validateManual(ctx, tenantID, nodeClient, nodeName)
	case "auto":
		// Roll back to failed: we claimed registering but cannot proceed.
		_ = s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).
			Update("tms_node_registration_status", lifecycle.PrereqFailed).Error
		return ErrAutoModeNotSupported
	default:
		_ = s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).
			Update("tms_node_registration_status", lifecycle.PrereqFailed).Error
		return fmt.Errorf("RegisterTmsNode: unknown mode %q; expected 'manual'", mode)
	}
}

// validateManual checks the structure of an operator-created TMS node.
// The registering status has already been atomically claimed by the caller
// (RegisterTmsNode transaction); this function only validates the node via
// the TMS API and writes failed on any error.
//
// Flow:
//  1. GetNodeByName → node must exist
//  2. Node must have at least one target with a non-empty DestinationName (structural check)
//
// Note: the target destination's existence and credentials are NOT validated via
// the Destination Service.  cpi-delivery's ProviderDest points to cpi-delivery's
// own subaccount Destination Service, whereas TMS node target destinations live
// in the TMS subaccount Destination Service — two separate instances in a
// multi-subaccount BTP deployment.  Destination validity is the operator's
// responsibility (B2 resolution).
func (s *Service) validateManual(ctx context.Context, tenantID uint, nodeClient *tms.TmsClient, nodeName string) error {
	if nodeName == "" {
		return s.writeTmsStatus(tenantID, lifecycle.PrereqFailed,
			fmt.Errorf("manual mode requires a non-empty nodeName"))
	}

	node, err := nodeClient.GetNodeByName(ctx, nodeName)
	if err != nil {
		return s.writeTmsStatus(tenantID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS API error looking up node %q: %w", nodeName, err))
	}
	if node == nil {
		return s.writeTmsStatus(tenantID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS node %q does not exist", nodeName))
	}

	// Structural check: node must have at least one target with a destination name.
	if len(node.Targets) == 0 {
		return s.writeTmsStatus(tenantID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS node %q has no targets", nodeName))
	}
	if node.Targets[0].DestinationName == "" {
		return s.writeTmsStatus(tenantID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS node %q target[0] has no destinationName", nodeName))
	}

	return nil
}

// ── ConfirmTmsRoutes ──────────────────────────────────────────────────────────

// ConfirmTmsRoutes is called when the operator asserts that they have completed
// Route configuration in the TMS UI.
//
//   - Fetches the current Route list from TMS (source = TmsSourceNodeName).
//   - Route list empty → returns ErrRoutesNotConfigured; status stays registering.
//   - Route list non-empty → writes TmsNodeRegistrationStatus = ready.
func (s *Service) ConfirmTmsRoutes(ctx context.Context, tenantID uint) (*ConfirmTmsRoutesResult, error) {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return nil, err
	}

	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: get central TMS context: %w", err)
	}

	routes, err := s.fetchRoutesForNode(ctx, tmsCtx, tenant.TmsSourceNodeName)
	if err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: fetch routes: %w", err)
	}

	if len(routes) == 0 {
		return nil, ErrRoutesNotConfigured
	}

	// Routes confirmed — write ready and bind to the CentralTmsContext.
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).Updates(map[string]any{
		"tms_node_registration_status": lifecycle.PrereqReady,
		"central_tms_context_id":       tmsCtx.ID,
	}).Error; err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: write ready: %w", err)
	}

	return &ConfirmTmsRoutesResult{Routes: routes}, nil
}

// ── GetTmsRoutes ──────────────────────────────────────────────────────────────

// GetTmsRoutes fetches the live Route list from TMS for the tenant's source node.
// It reads TmsSourceNodeName from the tenant, looks up the node by name to get
// its numeric ID, then calls ListRoutesBySourceNode.
func (s *Service) GetTmsRoutes(ctx context.Context, tenantID uint) ([]tms.V1TransportRoute, error) {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return nil, err
	}
	if tenant.TmsSourceNodeName == "" {
		return nil, fmt.Errorf("GetTmsRoutes: tenant %d has no TmsSourceNodeName", tenantID)
	}

	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return nil, fmt.Errorf("GetTmsRoutes: get central TMS context: %w", err)
	}

	return s.fetchRoutesForNode(ctx, tmsCtx, tenant.TmsSourceNodeName)
}

// fetchRoutesForNode builds a TMS client and returns the Route list for nodeName.
// Shared by GetTmsRoutes and ConfirmTmsRoutes so that ConfirmTmsRoutes can reuse
// the tmsCtx it already holds without an extra DB round-trip.
func (s *Service) fetchRoutesForNode(ctx context.Context, tmsCtx *db.CentralTmsContext, nodeName string) ([]tms.V1TransportRoute, error) {
	nodeClient, err := buildTmsClient(ctx, tmsCtx, s.ProviderDest)
	if err != nil {
		return nil, fmt.Errorf("fetchRoutesForNode: build TMS client: %w", err)
	}

	node, err := nodeClient.GetNodeByName(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("fetchRoutesForNode: look up node %q: %w", nodeName, err)
	}
	if node == nil {
		return nil, fmt.Errorf("fetchRoutesForNode: node %q not found in TMS", nodeName)
	}

	return nodeClient.ListRoutesBySourceNode(ctx, node.ID)
}

// writeTmsStatus writes failed status and returns the original error, so callers
// can do:
//
//	return s.writeTmsStatus(id, lifecycle.PrereqFailed, err)
//
// The DB write is best-effort: if it fails the error is logged but the original
// error is still returned (consistent with Phase 2 finish()/fail() pattern).
func (s *Service) writeTmsStatus(tenantID uint, status lifecycle.PrerequisiteStatus, origErr error) error {
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).
		Update("tms_node_registration_status", status).Error; err != nil {
		env.Logger().Errorw("writeTmsStatus: failed to persist TMS registration status",
			"tenantID", tenantID, "targetStatus", status, "error", err)
	}
	return origErr
}
