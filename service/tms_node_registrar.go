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
// On success TmsNodeRegistrationStatus is written to registering and TmsSourceNodeName
// is set (or updated if different from the stored value).
// On failure TmsNodeRegistrationStatus is written to failed.
func (s *Service) RegisterTmsNode(ctx context.Context, tenantID uint, mode, nodeName string) error {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return err
	}

	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return fmt.Errorf("RegisterTmsNode: get central TMS context: %w", err)
	}

	nodeClient, err := buildTmsClient(ctx, tmsCtx, s.ProviderDest)
	if err != nil {
		return fmt.Errorf("RegisterTmsNode: build TMS node client: %w", err)
	}

	switch mode {
	case "manual":
		return s.registerManual(ctx, tenant, tmsCtx, nodeClient, nodeName)
	case "auto":
		return ErrAutoModeNotSupported
	default:
		return fmt.Errorf("RegisterTmsNode: unknown mode %q; expected 'manual'", mode)
	}
}

// registerManual validates an operator-created TMS node and writes registering status.
//
// Flow:
//  1. GetNodeByName → node must exist
//  2. Node must have at least one target with a non-empty DestinationName (structural check)
//  3. Write registering
//
// Note: the target destination's existence and credentials are NOT validated via
// the Destination Service.  cpi-delivery's ProviderDest points to cpi-delivery's
// own subaccount Destination Service, whereas TMS node target destinations live
// in the TMS subaccount Destination Service — two separate instances in a
// multi-subaccount BTP deployment.  Destination validity is the operator's
// responsibility (B2 resolution).
func (s *Service) registerManual(ctx context.Context, tenant *db.CpiTenant, tmsCtx *db.CentralTmsContext, nodeClient *tms.TmsClient, nodeName string) error {
	if nodeName == "" {
		return s.writeTmsStatus(tenant.ID, lifecycle.PrereqFailed,
			fmt.Errorf("manual mode requires a non-empty nodeName"))
	}

	node, err := nodeClient.GetNodeByName(ctx, nodeName)
	if err != nil {
		return s.writeTmsStatus(tenant.ID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS API error looking up node %q: %w", nodeName, err))
	}
	if node == nil {
		return s.writeTmsStatus(tenant.ID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS node %q does not exist", nodeName))
	}

	// Structural check: node must have at least one target with a destination name.
	if len(node.Targets) == 0 {
		return s.writeTmsStatus(tenant.ID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS node %q has no targets", nodeName))
	}
	if node.Targets[0].DestinationName == "" {
		return s.writeTmsStatus(tenant.ID, lifecycle.PrereqFailed,
			fmt.Errorf("config_mismatch: TMS node %q target[0] has no destinationName", nodeName))
	}

	// Write registering — node structure confirmed, awaiting Route configuration.
	return s.writeTmsRegistering(ctx, tenant.ID, nodeName)
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

	routes, err := s.GetTmsRoutes(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: fetch routes: %w", err)
	}

	if len(routes) == 0 {
		return nil, ErrRoutesNotConfigured
	}

	// Routes confirmed — write ready and bind to the CentralTmsContext.
	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: get central TMS context: %w", err)
	}
	ctxID := tmsCtx.ID
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).Updates(map[string]any{
		"tms_node_registration_status": lifecycle.PrereqReady,
		"central_tms_context_id":       ctxID,
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

	nodeClient, err := buildTmsClient(ctx, tmsCtx, s.ProviderDest)
	if err != nil {
		return nil, fmt.Errorf("GetTmsRoutes: build TMS node client: %w", err)
	}

	node, err := nodeClient.GetNodeByName(ctx, tenant.TmsSourceNodeName)
	if err != nil {
		return nil, fmt.Errorf("GetTmsRoutes: look up node %q: %w", tenant.TmsSourceNodeName, err)
	}
	if node == nil {
		return nil, fmt.Errorf("GetTmsRoutes: node %q not found in TMS", tenant.TmsSourceNodeName)
	}

	return nodeClient.ListRoutesBySourceNode(ctx, node.ID)
}

// writeTmsRegistering sets TmsNodeRegistrationStatus = registering and
// TmsSourceNodeName on the tenant.
func (s *Service) writeTmsRegistering(ctx context.Context, tenantID uint, nodeName string) error {
	updates := map[string]any{
		"tms_node_registration_status": lifecycle.PrereqRegistering,
	}
	if nodeName != "" {
		updates["tms_source_node_name"] = nodeName
	}
	if err := s.DB.WithContext(ctx).Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(updates).Error; err != nil {
		return fmt.Errorf("writeTmsRegistering: %w", err)
	}
	return nil
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

