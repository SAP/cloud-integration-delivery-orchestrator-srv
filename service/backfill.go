package service

import (
	"context"
	"fmt"

	"mmt-delivery/db"
)

// BackfillResult summarizes the outcome of a BackfillArtifactTechIDs run.
type BackfillResult struct {
	Total   int                `json:"total"`
	Fixed   int                `json:"fixed"`
	Skipped int                `json:"skipped"`
	Failed  int                `json:"failed"`
	DryRun  bool               `json:"dryRun"`
	Details []BackfillOpDetail `json:"details"`
}

type BackfillOpDetail struct {
	OpID       uint   `json:"opID"`
	TenantID   uint   `json:"tenantID"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	OldTechID  string `json:"oldTechID"`
	NewTechID  string `json:"newTechID,omitempty"`
	Status     string `json:"status"` // fixed | skipped | failed
	Error      string `json:"error,omitempty"`
}

// BackfillArtifactTechIDs finds ops whose ArtifactTechID is likely a display name
// (equals ArtifactName or contains a space) and resolves the correct tech ID from CPI PIR.
// When dryRun is true, DB is not updated — only the result report is returned.
// tenantFilter=0 processes all tenants.
func (s *Service) BackfillArtifactTechIDs(ctx context.Context, dryRun bool, tenantFilter uint) (*BackfillResult, error) {
	if s.CPI == nil {
		return nil, fmt.Errorf("CPI factory not configured")
	}

	var ops []db.ArtifactTenantOperation
	q := s.DB.WithContext(ctx).
		Where("artifact_tech_id = artifact_name OR artifact_tech_id LIKE '% %'")
	if tenantFilter != 0 {
		q = q.Where("tenant_id = ?", tenantFilter)
	}
	if err := q.Find(&ops).Error; err != nil {
		return nil, fmt.Errorf("query candidate ops: %w", err)
	}

	// Build DR ID → source tenant map to avoid N+1 queries.
	// Tech ID is a design-time identifier — must be resolved via the source tenant's CPI,
	// not the target tenant's (target may not have the package at all).
	drIDs := make([]uint, 0, len(ops))
	for _, op := range ops {
		drIDs = append(drIDs, op.DeliveryRequestID)
	}
	var drs []db.DeliveryRequest
	if err := s.DB.WithContext(ctx).
		Select("id, source_tenant_id").
		Preload("SourceTenant").
		Where("id IN ?", drIDs).
		Find(&drs).Error; err != nil {
		return nil, fmt.Errorf("query delivery requests for source tenants: %w", err)
	}
	sourceTenantByDR := make(map[uint]db.CpiTenant, len(drs))
	for _, dr := range drs {
		sourceTenantByDR[dr.ID] = dr.SourceTenant
	}

	result := &BackfillResult{
		Total:  len(ops),
		DryRun: dryRun,
	}

	for _, op := range ops {
		detail := BackfillOpDetail{
			OpID:      op.ID,
			TenantID:  op.TenantID,
			Name:      op.ArtifactName,
			Version:   op.ArtifactVersion,
			OldTechID: op.ArtifactTechID,
		}

		sourceTenant := sourceTenantByDR[op.DeliveryRequestID]
		dest := sourceTenant.PirApiDestinationName
		if dest == "" {
			detail.Status = "skipped"
			detail.Error = fmt.Sprintf("source tenant for DR %d has no PirApiDestinationName (bootstrap required)", op.DeliveryRequestID)
			result.Skipped++
			result.Details = append(result.Details, detail)
			continue
		}

		cpiCli, err := s.CPI(ctx, dest)
		if err != nil {
			detail.Status = "failed"
			detail.Error = fmt.Sprintf("build CPI client: %v", err)
			result.Failed++
			result.Details = append(result.Details, detail)
			continue
		}

		techID, err := s.resolveTechID(ctx, cpiCli, op.PackageID, string(op.ArtifactType), op.ArtifactName, op.ArtifactVersion)
		if err != nil {
			detail.Status = "failed"
			detail.Error = err.Error()
			result.Failed++
			result.Details = append(result.Details, detail)
			continue
		}

		if techID == op.ArtifactTechID {
			detail.Status = "skipped"
			detail.Error = "tech ID already correct"
			result.Skipped++
			result.Details = append(result.Details, detail)
			continue
		}

		detail.NewTechID = techID
		if !dryRun {
			if err := s.DB.WithContext(ctx).Model(&db.ArtifactTenantOperation{}).
				Where("id = ?", op.ID).
				Update("artifact_tech_id", techID).Error; err != nil {
				detail.Status = "failed"
				detail.Error = fmt.Sprintf("DB update: %v", err)
				result.Failed++
				result.Details = append(result.Details, detail)
				continue
			}
		}
		detail.Status = "fixed"
		result.Fixed++
		result.Details = append(result.Details, detail)
	}

	return result, nil
}
