package service

// tms_node_registrar.go — TMS Node registration independent lifecycle (Phase 3).
//
// TMS Node registration is a synchronous HTTP operation separate from the
// bootstrap apply/retry lifecycle.  It is triggered by the operator after
// CF identity is configured (LifecycleState >= configured) via the /tms-node/* endpoints.
//
// Flow:
//   - RegisterTmsNode: operator selects a TMS node; backend atomically stores
//     nodeId/nodeName and sets TmsNodeRegistrationStatus = registering.
//     No TMS API call is made — node validity is the operator's responsibility.
//   - ConfirmTmsRoutes: operator asserts that they have configured a Route in the
//     TMS UI.  Backend fetches the live route list; if non-empty, status → ready.
//     If empty, 400 ROUTES_NOT_CONFIGURED is returned and status stays registering.
//
// Design reference: 02-bootstrap-state-and-data-model.md § "TMS Node 注册独立生命周期"
//                   05-detailed-execution-tasks.md § 3.1–3.3

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

// ── Public errors ─────────────────────────────────────────────────────────────

// ErrNodeNotRegistered is returned by GetCurrentNodeRoutes when no TMS node
// has been registered for the tenant yet (TmsSourceNodeID == 0).
var ErrNodeNotRegistered = fmt.Errorf("routes can only be queried after a TMS node has been registered")

// calls confirm but the Route list fetched from TMS is empty.  The caller
// should translate this to a 400 HTTP response with error ROUTES_NOT_CONFIGURED.
var ErrRoutesNotConfigured = fmt.Errorf("routes not configured: no routes found for this source node")

// ErrAlreadyRegistering is returned when RegisterTmsNode detects that
// TmsNodeRegistrationStatus is already registering inside its atomic
// SELECT FOR UPDATE guard, preventing a double-registration race.
var ErrAlreadyRegistering = fmt.Errorf("TMS node is already in registering state; complete Route configuration and call confirm, or wait before re-registering")

// ── ConfirmTmsRoutesResult ────────────────────────────────────────────────────

// ConfirmTmsRoutesResult holds the routes returned by a successful confirm call.
type ConfirmTmsRoutesResult struct {
	Routes []db.TransportRoute
}

// ── RegisterTmsNode ───────────────────────────────────────────────────────────

// RegisterTmsNode persists the operator-selected TMS node for the given tenant.
//
// nodeId is the numeric TMS node ID selected by the operator from the node list.
// nodeName is the node's name, stored as TmsSourceNodeName for CAS export requests.
//
// Atomically claims TmsNodeRegistrationStatus = registering via SELECT FOR UPDATE
// before writing nodeId/nodeName, preventing double-registration races.
// Returns ErrAlreadyRegistering if another request already claimed the slot.
//
// No TMS API validation is performed: node existence and target configuration
// are the operator's responsibility in the TMS system.  Issues will surface
// naturally when ConfirmTmsRoutes checks for routes.
func (s *Service) RegisterTmsNode(ctx context.Context, tenantID uint, nodeId uint, nodeName string) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t db.CpiTenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "tms_node_registration_status").
			First(&t, tenantID).Error; err != nil {
			return err
		}
		if t.TmsNodeRegistrationStatus == lifecycle.PrereqRegistering {
			return ErrAlreadyRegistering
		}
		return tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
			"tms_node_registration_status": lifecycle.PrereqRegistering,
			"tms_source_node_name":         nodeName,
			"tms_source_node_id":           nodeId,
		}).Error
	})
}

// ── ConfirmTmsRoutes ──────────────────────────────────────────────────────────

// ConfirmTmsRoutes is called when the operator asserts that they have completed
// Route configuration in the TMS UI.
//
//   - Fetches all Routes where nodeID is source or target.
//   - Route list empty → returns ErrRoutesNotConfigured; status stays registering.
//   - Route list non-empty → writes TmsNodeRegistrationStatus = ready.
func (s *Service) ConfirmTmsRoutes(ctx context.Context, tenantID uint, nodeID uint) (*ConfirmTmsRoutesResult, error) {
	if nodeID == 0 {
		return nil, fmt.Errorf("ConfirmTmsRoutes: nodeID is required")
	}

	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: get central TMS context: %w", err)
	}

	routes, err := s.fetchRoutesForNodeID(ctx, tmsCtx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: fetch routes: %w", err)
	}

	if len(routes) == 0 {
		return nil, ErrRoutesNotConfigured
	}

	// Routes confirmed — write ready and bind to the CentralTmsContext.
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
		"tms_node_registration_status": lifecycle.PrereqReady,
		"central_tms_context_id":       tmsCtx.ID,
	}).Error; err != nil {
		return nil, fmt.Errorf("ConfirmTmsRoutes: write ready: %w", err)
	}

	return &ConfirmTmsRoutesResult{Routes: routes}, nil
}

// ── GetCurrentNodeRoutes ──────────────────────────────────────────────────────

// GetCurrentNodeRoutesResult holds the result of a successful GetCurrentNodeRoutes call.
type GetCurrentNodeRoutesResult struct {
	NodeName string
	Routes   []db.TransportRoute
}

// GetCurrentNodeRoutes fetches all Routes where the tenant's registered
// TmsSourceNodeID appears as either source or target.
//
// Returns ErrNodeNotRegistered if no TMS node has been registered yet.
func (s *Service) GetCurrentNodeRoutes(ctx context.Context, tenantID uint) (*GetCurrentNodeRoutesResult, error) {
	var tenant db.CpiTenant
	if err := s.DB.Select("id", "tms_source_node_id", "tms_source_node_name").
		First(&tenant, tenantID).Error; err != nil {
		return nil, err
	}
	if tenant.TmsSourceNodeID == 0 {
		return nil, ErrNodeNotRegistered
	}

	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return nil, fmt.Errorf("GetCurrentNodeRoutes: get central TMS context: %w", err)
	}

	routes, err := s.fetchRoutesForNodeID(ctx, tmsCtx, tenant.TmsSourceNodeID)
	if err != nil {
		return nil, fmt.Errorf("GetCurrentNodeRoutes: fetch routes: %w", err)
	}
	return &GetCurrentNodeRoutesResult{NodeName: tenant.TmsSourceNodeName, Routes: routes}, nil
}

// fetchRoutesForNodeID builds a TMS client and returns all v2 routes where
// nodeID appears as source or target. Shared by GetTmsRoutes and ConfirmTmsRoutes.
func (s *Service) fetchRoutesForNodeID(ctx context.Context, tmsCtx *db.CentralTmsContext, nodeID uint) ([]db.TransportRoute, error) {
	tmsClient, err := buildTmsClient(ctx, tmsCtx, s.ProviderDest)
	if err != nil {
		return nil, fmt.Errorf("fetchRoutesForNodeID: build TMS client: %w", err)
	}
	all, err := tmsClient.GetRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetchRoutesForNodeID: get routes: %w", err)
	}
	var result []db.TransportRoute
	for _, r := range all {
		if r.SourceNodeID == nodeID || r.TargetNodeID == nodeID {
			result = append(result, r)
		}
	}
	return result, nil
}
