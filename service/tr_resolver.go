package service

// tr_resolver.go — CAS-based Transport Request generation (Phase 4).
//
// GenerateTransportRequest owns the full TR creation flow end-to-end:
//   1. Load tenant (readiness gate)
//   2. Load DeliveryRequest + ArtifactTenantOperations from DB
//   3. Build per-tenant CasClient via s.CAS
//   4. Fetch CAS catalog (ListContentResources) to resolve artifact GUIDs + package metadata
//   5. For each op independently:
//      a. Build single-artifact ContentResource (one artifact per TR, by design)
//      b. TriggerExport → poll until FINISHED → read transportRequestID from config
//      c. Write TR ID back to this op immediately
//   6. Return all results; if any op failed, return combined error alongside successes.
//
// One artifact per TR is enforced regardless of what CAS supports. This ensures
// TrExist validation (which assumes TR.Content[0].Metadata describes one artifact)
// remains correct and TR provenance stays unambiguous.

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cas"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/lifecycle"
)

// casIndexEntry maps an artifact tech ID to its catalog component and parent package.
type casIndexEntry struct {
	comp cas.CatalogComponent
	pkg  *cas.CatalogContentResource
}

// TransportRequest is the result of a successful export for one artifact operation.
type TransportRequest struct {
	ID  string // transportRequestID — written to ArtifactTenantOperation.TransportRequestNumber
	URL string // transportRequestURL — for audit / frontend navigation
}

const (
	casExportPollInterval = 3 * time.Second
	casExportTimeout      = 3 * time.Minute
)

// acquireTenantForTR atomically validates that the tenant is ready for TR
// generation using SELECT FOR UPDATE, preventing concurrent state transitions
// from racing past the readiness gate.
// Note: SQLite (used in tests) silently ignores FOR UPDATE; the lock guarantee
// is production-only (PostgreSQL).
func (s *Service) acquireTenantForTR(ctx context.Context, tenantID uint) (db.CpiTenant, error) {
	var tenant db.CpiTenant
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&tenant, tenantID).Error; err != nil {
			return fmt.Errorf("load tenant %d: %w", tenantID, err)
		}
		if tenant.LifecycleState != lifecycle.TenantReady {
			return fmt.Errorf("tenant %d lifecycleState=%q, must be ready", tenantID, tenant.LifecycleState)
		}
		if tenant.TmsNodeRegistrationStatus != lifecycle.PrereqReady {
			return fmt.Errorf("tenant %d tmsNodeRegistrationStatus=%q, must be ready", tenantID, tenant.TmsNodeRegistrationStatus)
		}
		if tenant.TmsSourceNodeName == "" {
			return fmt.Errorf("tenant %d has no TmsSourceNodeName", tenantID)
		}
		return nil
	})
	if err != nil {
		return db.CpiTenant{}, fmt.Errorf("GenerateTransportRequest: readiness gate: %w", err)
	}
	return tenant, nil
}

// GenerateTransportRequest creates one TMS Transport Request per artifact operation
// via the CAS export API and writes each TR ID back immediately upon success.
//
// Returns (succeeded, failed, fatalErr).
// fatalErr is non-nil when a pre-op step fails and no TR was attempted.
// succeeded and failed are per-op results; callers must inspect both —
// succeeded TRs are already persisted and must not be re-created.
func (s *Service) GenerateTransportRequest(ctx context.Context, tenantID, deliveryRequestID uint, artifactOperationIDs []uint) (map[uint]*TransportRequest, map[uint]error, error) {
	// ── 1. Atomic readiness gate (SELECT FOR UPDATE) ─────────────────────────
	tenant, err := s.acquireTenantForTR(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}

	// ── 2. Load DeliveryRequest ───────────────────────────────────────────────
	var dr db.DeliveryRequest
	if err := s.DB.WithContext(ctx).First(&dr, deliveryRequestID).Error; err != nil {
		return nil, nil, fmt.Errorf("GenerateTransportRequest: load delivery request %d: %w", deliveryRequestID, err)
	}

	// ── 3. Load ArtifactTenantOperations ─────────────────────────────────────
	var ops []db.ArtifactTenantOperation
	if err := s.DB.WithContext(ctx).
		Preload("Artifact").
		Where("id IN ? AND delivery_request_id = ? AND tenant_id = ?",
			artifactOperationIDs, deliveryRequestID, tenantID).
		Find(&ops).Error; err != nil {
		return nil, nil, fmt.Errorf("GenerateTransportRequest: load operations: %w", err)
	}
	if len(ops) == 0 {
		return nil, nil, fmt.Errorf("GenerateTransportRequest: no matching operations for tenant %d, DR %d", tenantID, deliveryRequestID)
	}

	// ── 4. Build per-tenant CasClient ────────────────────────────────────────
	casClient, err := s.CAS(ctx, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("GenerateTransportRequest: build CAS client: %w", err)
	}

	// ── 5. Fetch CAS catalog + build lookup index ─────────────────────────────
	catalog, err := casClient.ListContentResources(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("GenerateTransportRequest: fetch CAS catalog: %w", err)
	}

	index := make(map[string]casIndexEntry, len(catalog)*8)
	for i := range catalog {
		if catalog[i].SubType != "package" {
			continue
		}
		for _, comp := range catalog[i].Components {
			index[comp.Name] = casIndexEntry{comp: comp, pkg: &catalog[i]}
		}
	}

	// ── 6. Per-op: one export → one TR (concurrent) ─────────────────────────
	// Each artifact's CAS export is an independent async operation — run them
	// concurrently so total wait ≈ max(individual waits) instead of their sum.
	type opResult struct {
		opID uint
		tr   *TransportRequest
		err  error
	}
	resultCh := make(chan opResult, len(ops))

	for _, op := range ops {
		go func() {
			tr, err := s.exportOneTR(ctx, casClient, &tenant, &dr, op, index)
			resultCh <- opResult{opID: op.ID, tr: tr, err: err}
		}()
	}

	succeeded := make(map[uint]*TransportRequest, len(ops))
	failed := make(map[uint]error)

	for range ops {
		r := <-resultCh
		if r.err != nil {
			failed[r.opID] = r.err
			continue
		}

		// Write TR ID back immediately. If this fails the TR is orphaned in TMS
		// but not visible in DB; treat the op as failed so callers can retry.
		if dbErr := s.DB.WithContext(ctx).Model(&db.ArtifactTenantOperation{}).
			Where("id = ?", r.opID).
			Update("transport_request_number", r.tr.ID).Error; dbErr != nil {
			env.Logger().Errorw("GenerateTransportRequest: TR created but write-back failed (orphan TR)",
				"trID", r.tr.ID, "opID", r.opID, "error", dbErr)
			failed[r.opID] = fmt.Errorf("TR %s created in TMS but write-back failed: %w", r.tr.ID, dbErr)
			continue
		}

		succeeded[r.opID] = r.tr
	}

	return succeeded, failed, nil
}

// exportOneTR runs the full CAS export flow for a single artifact operation:
// build ContentResource → TriggerExport → poll → return TR.
func (s *Service) exportOneTR(
	ctx context.Context,
	casClient CasService,
	tenant *db.CpiTenant,
	dr *db.DeliveryRequest,
	op db.ArtifactTenantOperation,
	index map[string]casIndexEntry,
) (*TransportRequest, error) {
	techID := op.ArtifactTechID
	if techID == "" {
		return nil, fmt.Errorf("operation %d has no ArtifactTechID", op.ID)
	}
	entry, ok := index[techID]
	if !ok {
		return nil, fmt.Errorf("artifact %q not found in CAS catalog", techID)
	}

	contentResource := cas.ContentResource{
		ID:          entry.pkg.ID,
		ResourceID:  entry.pkg.ResourceID,
		ContentType: "Cloud Integration",
		SubType:     "package",
		Type:        "Cloud Integration",
		Name:        entry.pkg.Name,
		Version:     entry.pkg.Version,
		Components: []cas.ContentResourceComponent{
			{
				ID:                   entry.comp.ID,
				Name:                 entry.comp.Name,
				Type:                 entry.comp.Type,
				Version:              op.ArtifactVersion,
				Selected:             true,
				Enabled:              true,
				Mandatory:            false,
				DefaultSelect:        false,
				AdditionalProperties: nil,
				Exportable:           entry.comp.Exportable,
			},
		},
		Dependencies: []any{},
		MtaDescriptorSpecific: cas.MtaDescriptorSpecific{
			DeployedAfter: []any{},
		},
	}

	description := fmt.Sprintf("DR#%d | %s %s | Requested by: %s", dr.ID, op.Artifact.Name, op.ArtifactVersion, dr.CreatedBy)

	exportReq := cas.ExportRequest{
		Requestor:            "CPIDelivery",
		Version:              "1.0.0",
		ExportMode:           "TransportManagementService",
		ExportMediaType:      "MTAR",
		IsModifiable:         false,
		SourceNode:           tenant.TmsSourceNodeName,
		TransportDestination: "TransportManagementService",
		Description:          description,
		ContentResources:     []cas.ContentResource{contentResource},
	}

	exportResp, err := casClient.TriggerExport(ctx, exportReq)
	if err != nil {
		return nil, fmt.Errorf("trigger export for artifact %q: %w", techID, err)
	}
	processID := exportResp.ProcessID

	deadline := time.Now().Add(casExportTimeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("CAS export timed out after %v (artifact=%q, processId=%s)", casExportTimeout, techID, processID)
		}

		status, err := casClient.PollOperation(ctx, processID)
		if err != nil {
			return nil, fmt.Errorf("poll operation %s (artifact=%q): %w", processID, techID, err)
		}

		switch status.State {
		case "FINISHED":
			// fall through to config read below
		case "FAILED":
			return nil, fmt.Errorf("CAS export FAILED (artifact=%q, processId=%s)", techID, processID)
		default:
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("export poll cancelled (artifact=%q): %w", techID, ctx.Err())
			case <-time.After(casExportPollInterval):
			}
			continue
		}

		cfg, err := casClient.GetOperationConfig(ctx, processID)
		if err != nil {
			return nil, fmt.Errorf("get operation config %s (artifact=%q): %w", processID, techID, err)
		}
		if cfg.TransportRequestID == "" {
			return nil, fmt.Errorf("missing transportRequestID in config (artifact=%q, processId=%s)", techID, processID)
		}
		return &TransportRequest{ID: cfg.TransportRequestID, URL: cfg.TransportRequestURL}, nil
	}
}
