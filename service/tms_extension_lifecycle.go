package service

// tms_extension_lifecycle.go — TMS extension state machine (RFC 032 P0).
//
// The TMS extension tracks whether a tenant has completed the TMS-specific
// bootstrap steps (CAS instances, subscriber destinations, TMS node binding)
// needed to participate in TMS-mode delivery.
//
// Design mirror: follows the same pattern as tenant_lifecycle.go
// (event-driven transitions, SELECT FOR UPDATE, single write entry point).
// Side effects (resetting fields, writing node info) are handled by callers,
// not by the transition function itself.

import (
	"context"
	"errors"
	"fmt"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrTmsTransitionNotAllowed is returned when the requested TMS event
// is not a valid transition from the tenant's current TmsState.
var ErrTmsTransitionNotAllowed = errors.New("TMS transition not allowed")

// TmsEvent represents a trigger that causes a TmsExtensionState transition.
type TmsEvent string

const (
	// TmsEventNodeSelected — operator picks a TMS source node.
	// Valid from: not_started, node_confirmed, bootstrapped.
	TmsEventNodeSelected TmsEvent = "node_selected"

	// TmsEventApplyStarted — TMS extension bootstrap job begins.
	// Valid from: node_confirmed, bootstrapped.
	TmsEventApplyStarted TmsEvent = "tms_apply_started"

	// TmsEventApplyFinished — TMS extension bootstrap job completed successfully.
	// Valid from: bootstrapping.
	TmsEventApplyFinished TmsEvent = "tms_apply_finished"

	// TmsEventApplyFailed — TMS extension bootstrap job failed.
	// Valid from: bootstrapping.
	TmsEventApplyFailed TmsEvent = "tms_apply_failed"

	// TmsEventInvalidated — TMS extension was invalidated by rule 3 (PIR key
	// changed during BASE bootstrap re-apply).
	// Valid from: bootstrapped.
	// Note: rule 4 (Central TMS changed) performs a bulk UPDATE directly and does
	// not use this event — see handler/central_tms.go UpsertCentralTmsContext.
	TmsEventInvalidated TmsEvent = "tms_invalidated"

	// TmsEventReset — TMS extension reset to not_started (rule 1: CF identity
	// re-saved). Clears all TMS extension fields.
	// Valid from: all states except bootstrapping.
	TmsEventReset TmsEvent = "tms_reset"
)

// allowedTmsTransitions defines the valid (currentState, event) → nextState edges.
var allowedTmsTransitions = map[lifecycle.TmsExtensionState]map[TmsEvent]lifecycle.TmsExtensionState{
	lifecycle.TmsNotStarted: {
		TmsEventNodeSelected: lifecycle.TmsNodeConfirmed,
		TmsEventReset:        lifecycle.TmsNotStarted, // idempotent
	},
	lifecycle.TmsNodeConfirmed: {
		TmsEventNodeSelected: lifecycle.TmsNodeConfirmed, // re-select node
		TmsEventApplyStarted: lifecycle.TmsBootstrapping,
		TmsEventReset:        lifecycle.TmsNotStarted,
	},
	lifecycle.TmsBootstrapping: {
		TmsEventApplyFinished: lifecycle.TmsBootstrapped,
		TmsEventApplyFailed:   lifecycle.TmsNodeConfirmed,
	},
	lifecycle.TmsBootstrapped: {
		TmsEventNodeSelected: lifecycle.TmsNodeConfirmed, // re-select node
		TmsEventApplyStarted: lifecycle.TmsBootstrapping, // re-apply
		TmsEventInvalidated:  lifecycle.TmsNodeConfirmed,
		TmsEventReset:        lifecycle.TmsNotStarted,
	},
}

// SelectTmsNode atomically transitions to node_confirmed and writes node fields.
// Also unconditionally marks TransportManagement destination as stale (R2 fix).
// Rejects draft tenants (CF identity not yet configured).
func (s *Service) SelectTmsNode(ctx context.Context, tenantID uint, nodeID uint, nodeName string) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Draft check under lock — transitionTmsWithTx reads lifecycle_state
		// but does not enforce the LifecycleState ≠ draft precondition.
		var tenant db.CpiTenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "lifecycle_state").
			First(&tenant, tenantID).Error; err != nil {
			return fmt.Errorf("SelectTmsNode: fetch tenant %d: %w", tenantID, err)
		}
		// Reject draft: CF identity not yet configured. We intentionally check
		// != draft rather than IsOnboarded() — node selection only needs CF identity
		// to exist, not completed BASE bootstrap. This allows operators to pre-configure
		// TMS node while BASE bootstrap is pending. The IsOnboarded() gate is at
		// ApplyTmsExtension, not here.
		if tenant.LifecycleState == lifecycle.TenantDraft {
			return fmt.Errorf("SelectTmsNode: tenant %d is in draft state — complete CF Connection first", tenantID)
		}
		// transitionTmsWithTx re-locks the same row (no-op, already held).
		if err := s.transitionTmsWithTx(tx, tenantID, TmsEventNodeSelected); err != nil {
			return err
		}
		updates := map[string]any{
			"tms_source_node_name":             nodeName,
			"tms_source_node_id":               nodeID,
			"transport_management_dest_status": lifecycle.PrereqMissing,
			"tms_blocking_reason":              lifecycle.TmsReasonNodeChanged,
		}
		return tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(updates).Error
	})
}

// ── Route queries ───────────────────────────────────────────────────────────

// TransitionTms applies a TMS event within its own transaction.
// Convenience wrapper around transitionTmsWithTx.
func (s *Service) TransitionTms(tenantID uint, event TmsEvent) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		return s.transitionTmsWithTx(tx, tenantID, event)
	})
}

// transitionTmsWithTx validates and writes a TmsExtensionState transition.
//
// It locks the tenant row (SELECT FOR UPDATE), reads both lifecycle_state
// and tms_extension_state, verifies the event is valid from the current state,
// and writes the new tms_extension_state.
//
// Side effects (field resets, node writes, job creation) are the caller's
// responsibility — this function only guards and writes the state.
//
// Bypass exceptions: Rule 1 (SaveCfIdentity) and Rule 4 (UpsertCentralTmsContext)
// write tms_extension_state directly without calling this function.
// Rule 1 needs to atomically reset all TMS fields in one UPDATE within its own tx.
// Rule 4 performs a bulk UPDATE across all bootstrapped tenants.
// Both paths validate state constraints (Rule 1: != bootstrapping; Rule 4: WHERE bootstrapped)
// before writing.
func (s *Service) transitionTmsWithTx(tx *gorm.DB, tenantID uint, event TmsEvent) error {
	var tenant db.CpiTenant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "lifecycle_state", "tms_extension_state").
		First(&tenant, tenantID).Error; err != nil {
		return fmt.Errorf("transitionTmsWithTx: fetch tenant %d: %w", tenantID, err)
	}

	events, ok := allowedTmsTransitions[tenant.TmsExtensionState]
	if !ok {
		return fmt.Errorf("transitionTmsWithTx: no transitions defined for TmsExtensionState %q: %w",
			tenant.TmsExtensionState, ErrTmsTransitionNotAllowed)
	}

	nextState, ok := events[event]
	if !ok {
		return fmt.Errorf("transitionTmsWithTx: event %q is not valid from TmsExtensionState %q (tenant %d): %w",
			event, tenant.TmsExtensionState, tenantID, ErrTmsTransitionNotAllowed)
	}

	if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).
		Update("tms_extension_state", nextState).Error; err != nil {
		return fmt.Errorf("transitionTmsWithTx: write TmsExtensionState %q for tenant %d: %w",
			nextState, tenantID, err)
	}
	return nil
}

// ── Route queries ───────────────────────────────────────────────────────────

// ErrNodeNotRegistered is returned by GetCurrentNodeRoutes when no TMS node
// has been registered for the tenant yet (TmsSourceNodeID == 0).
var ErrNodeNotRegistered = fmt.Errorf("routes can only be queried after a TMS node has been registered")

// ErrRoutesNotConfigured is returned when the Route list fetched from TMS is empty.
var ErrRoutesNotConfigured = fmt.Errorf("routes not configured: no routes found for this source node")

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
// nodeID appears as source or target.
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
