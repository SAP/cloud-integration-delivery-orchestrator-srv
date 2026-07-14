package service

// tr_resolver.go — CAS-based Transport Request generation (Phase 4).
//
// GenerateTransportRequest owns the full TR creation flow end-to-end:
//   1. Load tenant (readiness gate)
//   2. Load DeliveryRequest + ArtifactTenantOperations from DB
//   3. Build per-tenant CasClient via s.CAS
//   4. GUID cache check: if all ops already have CasArtifactGUID populated,
//      skip CAS entirely; otherwise call ListCloudIntegrationResources, populate
//      the 5 new cache fields on ops+artifacts, and persist them to DB.
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
	"mmt-delivery/pkg/lifecycle"
)

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
		if tenant.TmsSourceNodeName == "" || tenant.TmsSourceNodeID == 0 {
			return fmt.Errorf("tenant %d has no TmsSourceNodeName or TmsSourceNodeID", tenantID)
		}
		return nil
	})
	if err != nil {
		return db.CpiTenant{}, fmt.Errorf("GenerateTransportRequest: readiness gate: %w", err)
	}
	return tenant, nil
}

// ensureCasGUIDs checks whether every op in the batch already has CasArtifactGUID
// populated. If all are present the function returns immediately (cache hit, no CAS call).
// If any are missing it calls ListCloudIntegrationResources once for the affected
// packages, fills in the 5 cache fields (3 on op, 2 on artifact), and persists them.
// On return, every op in the slice is guaranteed to have CasArtifactGUID set (or an
// error is returned).
func (s *Service) ensureCasGUIDs(ctx context.Context, casClient CasService, ops []db.ArtifactTenantOperation) error {
	// Fast path: all GUIDs already cached.
	allCached := true
	for i := range ops {
		if ops[i].CasArtifactGUID == "" {
			allCached = false
			break
		}
	}
	if allCached {
		return nil
	}

	// Slow path: fetch catalog for the affected packages.
	packageIDs := make([]string, 0, len(ops))
	seen := make(map[string]struct{}, len(ops))
	for _, op := range ops {
		if op.PackageID != "" {
			if _, ok := seen[op.PackageID]; !ok {
				seen[op.PackageID] = struct{}{}
				packageIDs = append(packageIDs, op.PackageID)
			}
		}
	}

	catalog, err := casClient.ListCloudIntegrationResources(ctx, packageIDs)
	if err != nil {
		return fmt.Errorf("fetch CAS catalog: %w", err)
	}

	// Build lookup: (packageID, artifactName) → {component, package}.
	// Only index entries that are actually needed by ops; duplicates within
	// unrelated artifacts in the same package are irrelevant.
	// Tech ID is NOT returned by CAS and is not used in the export flow.
	type entry struct {
		comp cas.CatalogComponent
		pkg  cas.CatalogContentResource
	}
	neededKeys := make(map[string]struct{}, len(ops))
	for _, op := range ops {
		neededKeys[op.PackageID+"::"+op.ArtifactName] = struct{}{}
	}
	index := make(map[string]entry, len(neededKeys))
	for _, res := range catalog {
		if res.SubType != "package" {
			continue
		}
		for _, comp := range res.Components {
			key := res.ID + "::" + comp.Name
			if _, needed := neededKeys[key]; !needed {
				continue
			}
			if _, exists := index[key]; exists {
				return fmt.Errorf("ambiguous artifact %q in package %q: multiple components share the same display name — rename to unique names within the package", comp.Name, res.ID)
			}
			index[key] = entry{comp: comp, pkg: res}
		}
	}

	// Populate cache fields and persist.
	for i := range ops {
		// Match by (packageID, artifactName) — TechID is not available in CAS catalog.
		key := ops[i].PackageID + "::" + ops[i].ArtifactName
		e, ok := index[key]
		if !ok {
			return fmt.Errorf("artifact %q (techID=%q) not found in CAS catalog", ops[i].ArtifactName, ops[i].ArtifactTechID)
		}

		// Populate cache fields in-memory and persist — overwrite unconditionally
		// since CAS is the authoritative source for these values.
		ops[i].CasArtifactGUID = e.comp.ID
		ops[i].CasPackageResourceID = e.pkg.ResourceID
		ops[i].CasArtifactExportable = e.comp.Exportable
		
		ops[i].PackageName = e.pkg.Name
		ops[i].PackageVersion = e.pkg.Version

		if err := s.DB.WithContext(ctx).Model(&ops[i]).Updates(map[string]any{
			"cas_artifact_guid":       e.comp.ID,
			"cas_package_resource_id": e.pkg.ResourceID,
			"cas_artifact_exportable": e.comp.Exportable,
			"package_name":            e.pkg.Name,
			"package_version":         e.pkg.Version,
		}).Error; err != nil {
			return fmt.Errorf("persist CAS GUIDs for op %d: %w", ops[i].ID, err)
		}
	}

	return nil
}

// GenerateTransportRequest creates one TMS Transport Request per artifact operation
// via the CAS export API and writes each TR ID back immediately upon success.
//
// Returns (succeeded, failed, fatalErr).
// fatalErr is non-nil when a pre-op step fails and no TR was attempted.
// succeeded and failed are per-op results; callers must inspect both —
// succeeded TRs are already persisted and must not be re-created.
func (s *Service) GenerateTransportRequest(ctx context.Context, tenantID, deliveryRequestID uint, artifactOperationIDs []uint) (map[uint]*TransportRequest, map[uint]error, error) {
	// ── 0. Set all target ops to TR_GENERATING unconditionally.
	//    Covers both the InsertOps path (NOT_REQUESTED) and the manual retry path (TR_FAILED).
	if dbErr := s.DB.WithContext(ctx).Model(&db.ArtifactTenantOperation{}).
		Where("id IN ?", artifactOperationIDs).
		Updates(map[string]any{
			"request_state": lifecycle.RequestTrGenerating,
			"tr_error":      "",
		}).Error; dbErr != nil {
		s.Logger.Warnw("GenerateTransportRequest: failed to set ops to TR_GENERATING", "error", dbErr)
	}

	// fatalf handles pre-op fatal errors: writes a DR-level Condition for observability,
	// resets all TR_GENERATING ops to TR_FAILED, notifies via WebSocket, and returns the error.
	fatalf := func(err error) (map[uint]*TransportRequest, map[uint]error, error) {
		_ = s.BatchInsertConditions([]db.Condition{{
			DeliveryRequestID: deliveryRequestID,
			State:             lifecycle.CondError,
			Message:           fmt.Sprintf("TR generation failed: %s", err.Error()),
		}})
		if dbErr := s.DB.WithContext(ctx).Model(&db.ArtifactTenantOperation{}).
			Where("id IN ?", artifactOperationIDs).
			Updates(map[string]any{
				"request_state": lifecycle.RequestTrFailed,
				"tr_error":      err.Error(),
			}).Error; dbErr != nil {
			s.Logger.Warnw("GenerateTransportRequest: failed to reset TR_GENERATING ops on fatal error", "error", dbErr)
		}
		s.NotifyDrUpdated(deliveryRequestID)
		return nil, nil, err
	}

	// ── 1. Atomic readiness gate (SELECT FOR UPDATE) ─────────────────────────
	tenant, err := s.acquireTenantForTR(ctx, tenantID)
	if err != nil {
		return fatalf(err)
	}

	// ── 2. Load DeliveryRequest ───────────────────────────────────────────────
	var dr db.DeliveryRequest
	if err := s.DB.WithContext(ctx).First(&dr, deliveryRequestID).Error; err != nil {
		return fatalf(fmt.Errorf("GenerateTransportRequest: load delivery request %d: %w", deliveryRequestID, err))
	}
	requester := dr.CreatedBy
	if email, err := s.GetUserEmail(ctx, dr.CreatedBy); err == nil {
		requester = email
	}

	// ── 3. Load ArtifactTenantOperations ─────────────────────────────────────
	var ops []db.ArtifactTenantOperation
	if err := s.DB.WithContext(ctx).
		Where("id IN ? AND delivery_request_id = ? AND tenant_id = ?",
			artifactOperationIDs, deliveryRequestID, tenantID).
		Find(&ops).Error; err != nil {
		return fatalf(fmt.Errorf("GenerateTransportRequest: load operations: %w", err))
	}
	if len(ops) == 0 {
		return fatalf(fmt.Errorf("GenerateTransportRequest: no matching operations for tenant %d, DR %d", tenantID, deliveryRequestID))
	}

	// ── 4. Build per-tenant CasClient ────────────────────────────────────────
	casClient, err := s.CAS(ctx, tenantID)
	if err != nil {
		return fatalf(fmt.Errorf("GenerateTransportRequest: build CAS client: %w", err))
	}

	// ── 5. GUID cache: skip CAS if all ops already have CasArtifactGUID ─────
	// On first run (or after a GUID invalidation) the cache fields are empty →
	// fall back to calling ListCloudIntegrationResources, populate, and persist.
	if err := s.ensureCasGUIDs(ctx, casClient, ops); err != nil {
		return fatalf(fmt.Errorf("GenerateTransportRequest: GUID cache: %w", err))
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
			defer func() {
				if r := recover(); r != nil {
					resultCh <- opResult{opID: op.ID, err: fmt.Errorf("panic during TR export for artifact %q: %v", op.ArtifactName, r)}
				}
			}()
			tr, err := s.exportOneTR(ctx, casClient, &tenant, &dr, op, requester)
			resultCh <- opResult{opID: op.ID, tr: tr, err: err}
		}()
	}

	succeeded := make(map[uint]*TransportRequest, len(ops))
	failed := make(map[uint]error)

	// Build opsByID for Condition messages.
	opsByID := make(map[uint]db.ArtifactTenantOperation, len(ops))
	for _, op := range ops {
		opsByID[op.ID] = op
	}

	for range ops {
		r := <-resultCh
		op := opsByID[r.opID]
		if r.err != nil {
			failed[r.opID] = r.err
			if dbErr := s.DB.WithContext(ctx).Model(&db.ArtifactTenantOperation{}).
				Where("id = ?", r.opID).
				Updates(map[string]any{
					"request_state": lifecycle.RequestTrFailed,
					"tr_error":      r.err.Error(),
				}).Error; dbErr != nil {
				s.Logger.Warnw("GenerateTransportRequest: failed to write TR_FAILED for op",
					"opID", r.opID, "error", dbErr)
			}
			_ = s.BatchInsertConditions([]db.Condition{{
				DeliveryRequestID: deliveryRequestID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("TR generation failed for %s %s: %s", op.ArtifactName, op.ArtifactVersion, r.err.Error()),
			}})
			continue
		}

		// Write TR ID back immediately and reset state to NOT_REQUESTED.
		if dbErr := s.DB.WithContext(ctx).Model(&db.ArtifactTenantOperation{}).
			Where("id = ?", r.opID).
			Updates(map[string]any{
				"transport_request_number": r.tr.ID,
				"request_state":            lifecycle.RequestPending,
				"tr_error":                 "",
			}).Error; dbErr != nil {
			s.Logger.Errorw("GenerateTransportRequest: TR created but write-back failed (orphan TR)",
				"trID", r.tr.ID, "opID", r.opID, "error", dbErr)
			failed[r.opID] = fmt.Errorf("TR %s created in TMS but write-back failed: %w", r.tr.ID, dbErr)
			continue
		}

		_ = s.BatchInsertConditions([]db.Condition{{
			DeliveryRequestID: deliveryRequestID,
			State:             lifecycle.CondSuccess,
			Message:           fmt.Sprintf("TR %s created for %s %s", r.tr.ID, op.ArtifactName, op.ArtifactVersion),
		}})
		succeeded[r.opID] = r.tr
	}

	s.NotifyDrUpdated(deliveryRequestID)
	return succeeded, failed, nil
}

// exportOneTR runs the full CAS export flow for a single artifact operation:
// build ContentResource from cached GUID fields → TriggerExport → poll → return TR.
// Precondition: op.CasArtifactGUID must be non-empty (ensureCasGUIDs guarantees this).
func (s *Service) exportOneTR(
	ctx context.Context,
	casClient CasService,
	tenant *db.CpiTenant,
	dr *db.DeliveryRequest,
	op db.ArtifactTenantOperation,
	requester string,
) (*TransportRequest, error) {
	techID := op.ArtifactTechID
	if techID == "" {
		return nil, fmt.Errorf("operation %d has no ArtifactTechID", op.ID)
	}
	if op.CasArtifactGUID == "" {
		return nil, fmt.Errorf("operation %d has no CasArtifactGUID (ensureCasGUIDs must be called first)", op.ID)
	}

	contentResource := cas.ContentResource{
		ID:          op.PackageID,
		ResourceID:  op.CasPackageResourceID,
		ContentType: "Cloud Integration",
		SubType:     "package",
		Type:        "Cloud Integration",
		Name:        op.PackageName,
		Version:     op.PackageVersion,
		Components: []cas.ContentResourceComponent{
			{
				ID:                   op.CasArtifactGUID,
				Name:                 op.ArtifactName,
				Type:                 string(op.ArtifactType),
				Version:              op.ArtifactVersion,
				Selected:             true,
				Enabled:              true,
				Mandatory:            false,
				DefaultSelect:        false,
				AdditionalProperties: nil,
				Exportable:           op.CasArtifactExportable,
			},
		},
		Dependencies: []any{},
		MtaDescriptorSpecific: cas.MtaDescriptorSpecific{
			DeployedAfter: []any{},
		},
	}

	description := fmt.Sprintf("DR#%d - %s %s - Requested by: %s", dr.ID, op.ArtifactName, op.ArtifactVersion, requester)

	exportReq := cas.ExportRequest{
		ID:                   fmt.Sprintf("%d", op.ID),
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
			// Go switch does not fall-through by default; execution resumes after the switch block.
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
