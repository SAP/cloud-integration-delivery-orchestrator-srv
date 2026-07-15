package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/lifecycle"

	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

// versionCompareCooldown is the minimum interval between triggers for the same rule.
const versionCompareCooldown = 5 * time.Minute

// AdhocCooldown is the minimum interval between adhoc version compare calls (global).
const AdhocCooldown = 5 * time.Minute

// Global cooldown state for adhoc version compare.
var (
	adhocLastRun time.Time
	adhocMu      sync.Mutex
)

// TriggerResult is returned immediately by TriggerVersionCompare.
type TriggerResult struct {
	Status consts.TriggerStatus `json:"status"`
}

// TriggerVersionCompare starts an asynchronous version snapshot collection for a Delivery Rule.
//
// Flow:
//  1. Load DeliveryRule with SourceTenant + IncludedTenants.
//  2. Rate-limit check: if a completed snapshot exists within cooldown, return rate_limited.
//  3. Atomic DB upsert: set status="running". If already running -> conflict.
//  4. Launch background goroutine (context.Background()) to collect snapshots.
//  5. Return immediately with trigger status.
func (s *Service) TriggerVersionCompare(ruleID uint, triggeredBy string) (TriggerResult, error) {
	// 1. Load rule with associations
	var rule db.DeliveryRule
	if err := s.DB.
		Preload("SourceTenant").
		Preload("IncludedTenants").
		First(&rule, ruleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return TriggerResult{}, fmt.Errorf("delivery rule %d not found", ruleID)
		}
		return TriggerResult{}, fmt.Errorf("failed to load delivery rule %d: %w", ruleID, err)
	}

	if rule.SourceTenantID == 0 {
		return TriggerResult{}, fmt.Errorf("delivery rule %d has no source tenant configured", ruleID)
	}

	// 2. Rate-limit check
	var existing db.VersionCompareSnapshot
	found := s.DB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: ruleID}).First(&existing).Error == nil
	if found && existing.Status == consts.SnapshotStatusCompleted && existing.CompletedAt != nil {
		if time.Since(*existing.CompletedAt) < versionCompareCooldown {
			return TriggerResult{Status: consts.TriggerStatusRateLimited}, nil
		}
	}

	// 3. Atomic concurrent protection via DB upsert
	now := time.Now()
	if found {
		// Try to update only if NOT already running
		result := s.DB.Model(&db.VersionCompareSnapshot{}).
			Where(&db.VersionCompareSnapshot{DeliveryRuleID: ruleID}).
			Where("status != ?", consts.SnapshotStatusRunning).
			Select("Status", "TriggeredAt", "TriggeredBy", "CompletedAt", "Error", "Data").
			Updates(db.VersionCompareSnapshot{
				Status:      consts.SnapshotStatusRunning,
				TriggeredAt: now,
				TriggeredBy: triggeredBy,
				CompletedAt: nil,
				Error:       "",
				Data:        db.SnapshotData{},
			})
		if result.Error != nil {
			return TriggerResult{}, fmt.Errorf("failed to update snapshot: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			// Already running
			return TriggerResult{Status: consts.TriggerStatusConflict}, nil
		}
	} else {
		// First trigger for this rule — create new record
		snapshot := db.VersionCompareSnapshot{
			DeliveryRuleID: ruleID,
			Status:         consts.SnapshotStatusRunning,
			TriggeredAt:    now,
			TriggeredBy:    triggeredBy,
		}
		if err := s.DB.Create(&snapshot).Error; err != nil {
			return TriggerResult{}, fmt.Errorf("failed to create snapshot: %w", err)
		}
	}

	// 4. Launch background goroutine
	go s.collectVersionSnapshot(rule)

	// 5. Return immediately
	return TriggerResult{Status: consts.TriggerStatusRunning}, nil
}

// collectVersionSnapshot is the background worker that fetches all artifact versions
// across all tenants and stores the result in the DB.
func (s *Service) collectVersionSnapshot(rule db.DeliveryRule) {
	ctx := context.Background()

	completeWithError := func(errMsg string) {
		now := time.Now()
		s.DB.Model(&db.VersionCompareSnapshot{}).
			Where(&db.VersionCompareSnapshot{DeliveryRuleID: rule.ID}).
			Select("Status", "CompletedAt", "Error").
			Updates(db.VersionCompareSnapshot{
				Status:      consts.SnapshotStatusFailed,
				CompletedAt: &now,
				Error:       errMsg,
			})
	}

	// Get CPI client for source tenant and fetch package list via PIR
	sourceTenant := rule.SourceTenant
	sourceCPI, err := s.CPI(ctx, sourceTenant.PirApiDestinationName)
	if err != nil {
		completeWithError(fmt.Sprintf("failed to get source tenant CPI client: %s", err))
		return
	}
	packages, err := sourceCPI.GetPackages(ctx)
	if err != nil {
		completeWithError(fmt.Sprintf("failed to list packages from source tenant via PIR: %s", err))
		return
	}

	// Apply global included packages whitelist filter
	if includeSet := s.loadIncludedPackageFilter(); includeSet != nil {
		var filtered []cpi.CPIPackage
		for _, pkg := range packages {
			if includeSet[pkg.ID] {
				filtered = append(filtered, pkg)
			}
		}
		packages = filtered
	}

	// Build tenant list: source + included tenants (excluding source to avoid duplication)
	allTenants := make([]db.CpiTenant, 0, len(rule.IncludedTenants))
	allTenants = append(allTenants, rule.SourceTenant)
	for _, t := range rule.IncludedTenants {
		if t.ID != rule.SourceTenantID {
			allTenants = append(allTenants, t)
		}
	}

	comparedTenantIDs := make([]uint, 0, len(allTenants)-1)
	for _, t := range allTenants {
		if t.ID != rule.SourceTenantID {
			comparedTenantIDs = append(comparedTenantIDs, t.ID)
		}
	}

	// Pre-fetch runtime artifacts for each tenant
	runtimeIndex := s.fetchRuntimeIndex(ctx, allTenants)

	// Fetch design-time artifacts per package per tenant
	pkgSnapshots := s.collectAllPackageSnapshots(ctx, packages, allTenants, runtimeIndex)

	// Assemble final snapshot data
	snapshotData := db.SnapshotData{
		SourceTenantID:  rule.SourceTenantID,
		ComparedTenants: comparedTenantIDs,
		Packages:        pkgSnapshots,
	}

	// Update DB with completed snapshot
	now := time.Now()
	s.DB.Model(&db.VersionCompareSnapshot{}).
		Where(&db.VersionCompareSnapshot{DeliveryRuleID: rule.ID}).
		Select("Status", "CompletedAt", "Data", "Error").
		Updates(db.VersionCompareSnapshot{
			Status:      consts.SnapshotStatusCompleted,
			CompletedAt: &now,
			Data:        snapshotData,
			Error:       "",
		})
}

// fetchRuntimeIndex pre-fetches runtime artifacts for each tenant in parallel.
// Returns map[tenantID] -> map[artifactID] -> RuntimeArtifact.
// Individual tenant failures are logged as warnings; an empty map is stored for that tenant.
func (s *Service) fetchRuntimeIndex(ctx context.Context, tenants []db.CpiTenant) map[uint]map[string]cpi.RuntimeArtifact {
	runtimeIndex := make(map[uint]map[string]cpi.RuntimeArtifact)
	var rtMu sync.Mutex
	rtGroup, rtCtx := errgroup.WithContext(ctx)

	for _, tenant := range tenants {
		tenant := tenant // capture loop variable
		rtGroup.Go(func() error {
			client, err := s.CPI(rtCtx, tenant.PirApiDestinationName)
			if err != nil {
				s.Logger.Warnw("failed to get CPI client", "component", "version_compare", "tenant", tenant.PirApiDestinationName, "tenant_id", tenant.ID, "error", err)
				// Store empty map, don't fail entire operation
				rtMu.Lock()
				runtimeIndex[tenant.ID] = make(map[string]cpi.RuntimeArtifact)
				rtMu.Unlock()
				return nil
			}
			artifacts, err := client.GetRuntimeArtifacts(rtCtx)
			if err != nil {
				s.Logger.Warnw("failed to get runtime artifacts", "component", "version_compare", "tenant", tenant.PirApiDestinationName, "tenant_id", tenant.ID, "error", err)
				rtMu.Lock()
				runtimeIndex[tenant.ID] = make(map[string]cpi.RuntimeArtifact)
				rtMu.Unlock()
				return nil
			}
			index := make(map[string]cpi.RuntimeArtifact, len(artifacts))
			for _, a := range artifacts {
				index[a.ID] = a
			}
			rtMu.Lock()
			runtimeIndex[tenant.ID] = index
			rtMu.Unlock()
			return nil
		})
	}
	_ = rtGroup.Wait()
	return runtimeIndex
}

// collectAllPackageSnapshots fetches design-time artifacts per package across all tenants in parallel.
// Individual package failures are logged as warnings and skipped.
func (s *Service) collectAllPackageSnapshots(ctx context.Context, packages []cpi.CPIPackage, tenants []db.CpiTenant, runtimeIndex map[uint]map[string]cpi.RuntimeArtifact) []db.PackageSnapshot {
	var pkgSnapshots []db.PackageSnapshot
	var pkgMu sync.Mutex
	pkgGroup, pkgCtx := errgroup.WithContext(ctx)
	// Limit concurrency to avoid overwhelming CPI APIs
	pkgGroup.SetLimit(10)

	for _, pkg := range packages {
		pkg := pkg
		pkgGroup.Go(func() error {
			pkgSnapshot, err := s.collectPackageSnapshot(pkgCtx, pkg.ID, tenants, runtimeIndex)
			if err != nil {
				s.Logger.Warnw("failed to collect package", "component", "version_compare", "package_id", pkg.ID, "error", err)
				// Don't fail the whole operation, skip this package
				return nil
			}
			pkgMu.Lock()
			pkgSnapshots = append(pkgSnapshots, pkgSnapshot)
			pkgMu.Unlock()
			return nil
		})
	}
	_ = pkgGroup.Wait()
	return pkgSnapshots
}

// collectPackageSnapshot fetches design-time artifacts for one package across all tenants
// and enriches them with runtime info from the pre-fetched index.
func (s *Service) collectPackageSnapshot(
	ctx context.Context,
	packageID string,
	tenants []db.CpiTenant,
	runtimeIndex map[uint]map[string]cpi.RuntimeArtifact,
) (db.PackageSnapshot, error) {

	// For each tenant, fetch design-time artifacts for this package via PIR
	type tenantArtifacts struct {
		tenantID  uint
		artifacts []PackageArtifact
		err       error
	}

	results := make([]tenantArtifacts, len(tenants))
	g, gctx := errgroup.WithContext(ctx)

	for i, tenant := range tenants {
		i, tenant := i, tenant
		g.Go(func() error {
			cpiClient, err := s.CPI(gctx, tenant.PirApiDestinationName)
			if err != nil {
				results[i] = tenantArtifacts{tenantID: tenant.ID, err: err}
				return nil // error tolerance: don't fail entire group
			}
			arts, err := GetPackageArtifacts(gctx, cpiClient, packageID)
			if err != nil {
				results[i] = tenantArtifacts{tenantID: tenant.ID, err: err}
				return nil
			}
			results[i] = tenantArtifacts{tenantID: tenant.ID, artifacts: arts}
			return nil
		})
	}
	_ = g.Wait()

	// Build artifact map: artifactID -> ArtifactSnapshot
	// Use the source tenant (first in tenants list) as the reference for artifact list
	artifactMap := make(map[string]*db.ArtifactSnapshot)
	var artifactOrder []string // preserve discovery order

	for _, result := range results {
		for _, art := range result.artifacts {
			if _, exists := artifactMap[art.TechID]; !exists {
				artifactMap[art.TechID] = &db.ArtifactSnapshot{
					ID:       art.TechID,
					Name:     art.Name,
					Type:     art.Type,
					Versions: make(map[uint]db.ArtifactVersionInfo),
				}
				artifactOrder = append(artifactOrder, art.TechID)
			}

			versionInfo := db.ArtifactVersionInfo{
				DesignTimeVersion: art.Version,
				ModifiedBy:        art.ModifiedBy,
				ModifiedAt:        art.ModifiedAt,
			}

			// Enrich with runtime info
			if rtMap, ok := runtimeIndex[result.tenantID]; ok {
				if rtArt, ok := rtMap[art.TechID]; ok {
					versionInfo.RuntimeVersion = rtArt.Version
					versionInfo.RuntimeStatus = string(rtArt.Status)
				}
			}

			artifactMap[art.TechID].Versions[result.tenantID] = versionInfo
		}

		// Record per-tenant errors as artifact-level errors
		if result.err != nil {
			// If we got an error but no artifacts, still record it
			// We create a synthetic entry if needed
			s.Logger.Warnw("tenant error for package", "component", "version_compare", "tenant_id", result.tenantID, "package_id", packageID, "error", result.err)
		}
	}

	// Convert map to ordered slice
	artifacts := make([]db.ArtifactSnapshot, 0, len(artifactOrder))
	for _, id := range artifactOrder {
		artifacts = append(artifacts, *artifactMap[id])
	}

	return db.PackageSnapshot{
		PackageID: packageID,
		Artifacts: artifacts,
	}, nil
}

// --- Query Functions ---

// VersionCompareQueryParams holds the filter parameters for QueryVersionCompare.
type VersionCompareQueryParams struct {
	PackageIDs   []string // filter by package IDs (empty = all)
	DesignTime   bool     // include design-time version info
	RunTime      bool     // include runtime version info
	MismatchOnly bool     // only show artifacts with mismatches
}

// VersionCompareResponse is the response for the query endpoint.
type VersionCompareResponse struct {
	Status      consts.SnapshotStatus      `json:"status"`
	TriggeredAt *time.Time                 `json:"triggeredAt,omitempty"`
	CompletedAt *time.Time                 `json:"completedAt,omitempty"`
	TriggeredBy string                     `json:"triggeredBy,omitempty"`
	Error       string                     `json:"error,omitempty"`
	Tenants     []VersionCompareTenantInfo `json:"tenants,omitempty"`
	Packages    []VersionComparePackage    `json:"packages,omitempty"`
}

// VersionCompareTenantInfo provides tenant metadata for the response.
type VersionCompareTenantInfo struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	IsSource bool   `json:"isSource"`
}

// VersionComparePackage is a package in the query response with computed match info.
type VersionComparePackage struct {
	PackageID string                   `json:"packageID"`
	Artifacts []VersionCompareArtifact `json:"artifacts"`
}

// VersionCompareArtifact is an artifact in the query response with per-tenant match info.
type VersionCompareArtifact struct {
	ID       string                                    `json:"id"`
	Name     string                                    `json:"name"`
	Type     string                                    `json:"type"`
	Versions map[uint]VersionCompareArtifactTenantInfo `json:"versions"` // key = tenant ID
}

// VersionCompareArtifactTenantInfo is version + match info for one artifact on one tenant.
type VersionCompareArtifactTenantInfo struct {
	DesignTimeVersion string `json:"designTimeVersion,omitempty"`
	DesignTimeMatch   *bool  `json:"designTimeMatch,omitempty"` // nil if designTime filter off
	DesignTimeDraft   bool   `json:"designTimeDraft,omitempty"`
	ModifiedBy        string `json:"modifiedBy,omitempty"` // source tenant only: last design-time committer
	ModifiedAt        string `json:"modifiedAt,omitempty"` // source tenant only: last design-time modification time
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	RuntimeMatch      *bool  `json:"runtimeMatch,omitempty"` // nil if runTime filter off
	RuntimeStatus     string `json:"runtimeStatus,omitempty"`
	Error             string `json:"error,omitempty"`
}

// QueryVersionCompare returns the cached snapshot with real-time match computation and filtering.
func (s *Service) QueryVersionCompare(ruleID uint, params VersionCompareQueryParams) (VersionCompareResponse, error) {
	var snapshot db.VersionCompareSnapshot
	if err := s.DB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: ruleID}).First(&snapshot).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return VersionCompareResponse{Status: consts.SnapshotStatusNone}, nil
		}
		return VersionCompareResponse{}, fmt.Errorf("failed to query snapshot: %w", err)
	}

	resp := VersionCompareResponse{
		Status:      snapshot.Status,
		TriggeredAt: &snapshot.TriggeredAt,
		CompletedAt: snapshot.CompletedAt,
		TriggeredBy: snapshot.TriggeredBy,
		Error:       snapshot.Error,
	}

	// If not completed, return status only (no data)
	if snapshot.Status != consts.SnapshotStatusCompleted {
		return resp, nil
	}

	// Load tenant metadata
	tenantIDs := append([]uint{snapshot.Data.SourceTenantID}, snapshot.Data.ComparedTenants...)
	var tenants []db.CpiTenant
	if err := s.DB.Where("id IN ?", tenantIDs).Find(&tenants).Error; err != nil {
		return resp, fmt.Errorf("failed to load tenants: %w", err)
	}

	result := buildVersionCompareResponse(snapshot.Data, tenants, snapshot.Data.SourceTenantID, params)
	result.Status = snapshot.Status
	result.TriggeredAt = &snapshot.TriggeredAt
	result.CompletedAt = snapshot.CompletedAt
	result.TriggeredBy = snapshot.TriggeredBy
	result.Error = snapshot.Error
	return result, nil
}

// buildVersionCompareResponse constructs a VersionCompareResponse from snapshot data and tenant info.
func buildVersionCompareResponse(data db.SnapshotData, tenants []db.CpiTenant, sourceTenantID uint, params VersionCompareQueryParams) VersionCompareResponse {
	var resp VersionCompareResponse

	tenantMap := make(map[uint]db.CpiTenant, len(tenants))
	for _, t := range tenants {
		tenantMap[t.ID] = t
	}

	// Build tenant info list (preserve order: source first, then compared)
	tenantIDs := append([]uint{sourceTenantID}, data.ComparedTenants...)
	for _, id := range tenantIDs {
		if t, ok := tenantMap[id]; ok {
			resp.Tenants = append(resp.Tenants, VersionCompareTenantInfo{
				ID:       t.ID,
				Name:     t.Name,
				IsSource: t.ID == sourceTenantID,
			})
		}
	}

	// Build packageID filter set
	pkgFilter := make(map[string]bool)
	for _, id := range params.PackageIDs {
		pkgFilter[id] = true
	}

	// Process packages with match computation
	for _, pkg := range data.Packages {
		// Apply package filter
		if len(pkgFilter) > 0 && !pkgFilter[pkg.PackageID] {
			continue
		}

		var filteredArtifacts []VersionCompareArtifact
		for _, art := range pkg.Artifacts {
			sourceVersion, sourceHasData := art.Versions[sourceTenantID]
			hasMismatch := false

			versions := make(map[uint]VersionCompareArtifactTenantInfo, len(art.Versions))
			for tenantID, vi := range art.Versions {
				info := VersionCompareArtifactTenantInfo{
					Error: vi.Error,
				}

				if params.DesignTime {
					info.DesignTimeVersion = vi.DesignTimeVersion
					info.DesignTimeDraft = strings.EqualFold(vi.DesignTimeVersion, "active")
					if tenantID == sourceTenantID {
						info.ModifiedBy = vi.ModifiedBy
						info.ModifiedAt = vi.ModifiedAt
					}
					if tenantID != sourceTenantID && sourceHasData {
						match := vi.DesignTimeVersion == sourceVersion.DesignTimeVersion
						info.DesignTimeMatch = &match
						if !match {
							hasMismatch = true
						}
					}
				}

				if params.RunTime {
					info.RuntimeVersion = vi.RuntimeVersion
					info.RuntimeStatus = vi.RuntimeStatus
					if tenantID != sourceTenantID && sourceHasData {
						match := vi.RuntimeVersion == sourceVersion.RuntimeVersion
						info.RuntimeMatch = &match
						if !match {
							hasMismatch = true
						}
					}
				}

				versions[tenantID] = info
			}

			// mismatchOnly filter
			if params.MismatchOnly && !hasMismatch {
				continue
			}

			filteredArtifacts = append(filteredArtifacts, VersionCompareArtifact{
				ID:       art.ID,
				Name:     art.Name,
				Type:     art.Type,
				Versions: versions,
			})
		}

		if len(filteredArtifacts) > 0 || !params.MismatchOnly {
			resp.Packages = append(resp.Packages, VersionComparePackage{
				PackageID: pkg.PackageID,
				Artifacts: filteredArtifacts,
			})
		}
	}

	return resp
}

// AdhocVersionCompare performs a synchronous version compare across the specified tenants.
// tenantIDs[0] is used as the source/baseline tenant.
// No data is persisted -- the result is returned directly.
func (s *Service) AdhocVersionCompare(ctx context.Context, tenantIDs []uint) (*VersionCompareResponse, error) {
	// 1. Global cooldown check
	adhocMu.Lock()
	if time.Since(adhocLastRun) < AdhocCooldown {
		adhocMu.Unlock()
		return nil, fmt.Errorf("rate_limited")
	}
	adhocLastRun = time.Now()
	adhocMu.Unlock()

	// 2. Load tenants from DB
	var tenants []db.CpiTenant
	if err := s.DB.Where("id IN ?", tenantIDs).Find(&tenants).Error; err != nil {
		return nil, fmt.Errorf("failed to load tenants: %w", err)
	}
	if len(tenants) < 2 {
		return nil, fmt.Errorf("at least 2 valid tenants required, found %d", len(tenants))
	}

	// Reorder tenants to match tenantIDs order (first = source)
	tenantMap := make(map[uint]db.CpiTenant)
	for _, t := range tenants {
		tenantMap[t.ID] = t
	}
	ordered := make([]db.CpiTenant, 0, len(tenantIDs))
	for _, id := range tenantIDs {
		if t, ok := tenantMap[id]; ok {
			ordered = append(ordered, t)
		}
	}
	tenants = ordered
	sourceTenantID := tenants[0].ID

	// 3. Get source CPI client and fetch package list via PIR
	sourceCPI, err := s.CPI(ctx, tenants[0].PirApiDestinationName)
	if err != nil {
		return nil, fmt.Errorf("failed to get source CPI client: %w", err)
	}
	packages, err := sourceCPI.GetPackages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list packages from source tenant via PIR: %w", err)
	}
	// No included packages filter for adhoc (compare all)

	// 4. Fetch runtime index
	runtimeIndex := s.fetchRuntimeIndex(ctx, tenants)

	// 5. Collect package snapshots
	pkgSnapshots := s.collectAllPackageSnapshots(ctx, packages, tenants, runtimeIndex)

	// 6. Build compared tenant IDs
	comparedTenantIDs := make([]uint, 0, len(tenants)-1)
	for _, t := range tenants[1:] {
		comparedTenantIDs = append(comparedTenantIDs, t.ID)
	}

	// 7. Build response
	snapshotData := db.SnapshotData{
		SourceTenantID:  sourceTenantID,
		ComparedTenants: comparedTenantIDs,
		Packages:        pkgSnapshots,
	}

	params := VersionCompareQueryParams{DesignTime: true, RunTime: true}
	resp := buildVersionCompareResponse(snapshotData, tenants, sourceTenantID, params)
	resp.Status = consts.SnapshotStatusCompleted
	now := time.Now()
	resp.CompletedAt = &now

	return &resp, nil
}

// --- Summary & Counts (for Rule card list and HomeView) ---

// VersionCompareSummaryItem represents one rule's snapshot summary (for the card list page).
type VersionCompareSummaryItem struct {
	DeliveryRuleID   uint                  `json:"deliveryRuleID"`
	DeliveryRuleName string                `json:"deliveryRuleName"`
	SourceTenantName string                `json:"sourceTenantName"`
	TenantCount      int                   `json:"tenantCount"`
	Status           consts.SnapshotStatus `json:"status"`
	TriggeredAt      *time.Time            `json:"triggeredAt,omitempty"`
	CompletedAt      *time.Time            `json:"completedAt,omitempty"`
	MatchedCount     int                   `json:"matchedCount"`
	MismatchedCount  int                   `json:"mismatchedCount"`
	TotalArtifacts   int                   `json:"totalArtifacts"`
}

// GetVersionCompareSummary returns snapshot summaries for all delivery rules (for the card list).
func (s *Service) GetVersionCompareSummary() ([]VersionCompareSummaryItem, error) {
	// Load all delivery rules with source tenant
	var rules []db.DeliveryRule
	if err := s.DB.
		Preload("SourceTenant").
		Preload("IncludedTenants").
		Where(&db.DeliveryRule{Active: true}).
		Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("failed to load delivery rules: %w", err)
	}

	// Load all snapshots
	var snapshots []db.VersionCompareSnapshot
	if err := s.DB.Find(&snapshots).Error; err != nil {
		return nil, fmt.Errorf("failed to load snapshots: %w", err)
	}
	snapshotMap := make(map[uint]*db.VersionCompareSnapshot, len(snapshots))
	for i := range snapshots {
		snapshotMap[snapshots[i].DeliveryRuleID] = &snapshots[i]
	}

	items := make([]VersionCompareSummaryItem, 0, len(rules))
	for _, rule := range rules {
		item := VersionCompareSummaryItem{
			DeliveryRuleID:   rule.ID,
			DeliveryRuleName: rule.Name,
			SourceTenantName: rule.SourceTenant.Name,
			TenantCount:      len(rule.IncludedTenants),
		}

		if snap, ok := snapshotMap[rule.ID]; ok {
			item.Status = snap.Status
			item.TriggeredAt = &snap.TriggeredAt
			item.CompletedAt = snap.CompletedAt

			if snap.Status == consts.SnapshotStatusCompleted {
				matched, mismatched, total := computeMismatchCounts(snap.Data)
				item.MatchedCount = matched
				item.MismatchedCount = mismatched
				item.TotalArtifacts = total
			}
		} else {
			item.Status = consts.SnapshotStatusNone
		}

		items = append(items, item)
	}

	return items, nil
}

// VersionCompareCounts provides rule-level mismatch statistics for the HomeView AppCard.
type VersionCompareCounts struct {
	Total        int            `json:"Total"`
	StatusCounts map[string]int `json:"StatusCounts"`
}

// GetVersionCompareCounts returns mismatch statistics across all rules (for HomeView AppCard).
func (s *Service) GetVersionCompareCounts() (VersionCompareCounts, error) {
	var rules []db.DeliveryRule
	if err := s.DB.Where(&db.DeliveryRule{Active: true}).Find(&rules).Error; err != nil {
		return VersionCompareCounts{}, fmt.Errorf("failed to load delivery rules: %w", err)
	}

	var snapshots []db.VersionCompareSnapshot
	if err := s.DB.Find(&snapshots).Error; err != nil {
		return VersionCompareCounts{}, fmt.Errorf("failed to load snapshots: %w", err)
	}
	snapshotMap := make(map[uint]*db.VersionCompareSnapshot, len(snapshots))
	for i := range snapshots {
		snapshotMap[snapshots[i].DeliveryRuleID] = &snapshots[i]
	}

	counts := VersionCompareCounts{
		Total:        len(rules),
		StatusCounts: make(map[string]int),
	}

	for _, rule := range rules {
		snap, ok := snapshotMap[rule.ID]
		if !ok {
			counts.StatusCounts[string(consts.SnapshotStatusNone)]++
			continue
		}
		if snap.Status != consts.SnapshotStatusCompleted {
			counts.StatusCounts[string(snap.Status)]++
			continue
		}

		_, mismatched, _ := computeMismatchCounts(snap.Data)
		if mismatched > 0 {
			counts.StatusCounts["mismatched"]++
		} else {
			counts.StatusCounts["matched"]++
		}
	}

	return counts, nil
}

// --- Included Packages (Global Whitelist) ---

// IncludedPackageInput is the input for a single included package entry.
type IncludedPackageInput struct {
	PackageID   string `json:"packageID"`
	Description string `json:"description"`
}

// GetIncludedPackages returns the global included packages whitelist.
func (s *Service) GetIncludedPackages() ([]db.VersionCompareIncludedPackage, error) {
	var packages []db.VersionCompareIncludedPackage
	if err := s.DB.Order("package_id ASC").Find(&packages).Error; err != nil {
		return nil, fmt.Errorf("failed to load included packages: %w", err)
	}
	return packages, nil
}

// UpdateIncludedPackages replaces the entire included packages list in a single transaction.
func (s *Service) UpdateIncludedPackages(inputs []IncludedPackageInput, updatedBy string) ([]db.VersionCompareIncludedPackage, error) {
	var result []db.VersionCompareIncludedPackage

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		// Hard-delete all existing entries (must bypass soft-delete so the unique index on PackageID is freed)
		if err := tx.Unscoped().Where("1 = 1").Delete(&db.VersionCompareIncludedPackage{}).Error; err != nil {
			return fmt.Errorf("failed to clear included packages: %w", err)
		}

		// Insert new entries
		for _, input := range inputs {
			pkg := db.VersionCompareIncludedPackage{
				PackageID:   input.PackageID,
				Description: input.Description,
				CreatedBy:   updatedBy,
			}
			if err := tx.Create(&pkg).Error; err != nil {
				return fmt.Errorf("failed to insert included package %s: %w", input.PackageID, err)
			}
			result = append(result, pkg)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// loadIncludedPackageFilter loads the global whitelist and returns a filter set.
// Returns nil if the whitelist is empty (meaning all packages should be included).
func (s *Service) loadIncludedPackageFilter() map[string]bool {
	var included []db.VersionCompareIncludedPackage
	s.DB.Find(&included)

	if len(included) == 0 {
		return nil // empty = compare all
	}

	includeSet := make(map[string]bool, len(included))
	for _, inc := range included {
		includeSet[inc.PackageID] = true
	}
	return includeSet
}

// --- Auto-Create DR from Version Compare Mismatch ---

// PreviewDRArtifact represents one artifact in the Preview response, categorized by eligibility.
type PreviewDRArtifact struct {
	ArtifactID    string          `json:"artifactID"`
	ArtifactName  string          `json:"artifactName"`
	PackageID     string          `json:"packageID"`
	Type          string          `json:"type"`
	SourceVersion string          `json:"sourceVersion"`
	ModifiedBy    string          `json:"modifiedBy,omitempty"`
	ModifiedAt    string          `json:"modifiedAt,omitempty"`
	Category      string          `json:"category"` // "includable" | "draft" | "versionPattern" | "duplicate"
	Reason        string          `json:"reason,omitempty"`
	ExistingDR    *ExistingDRInfo `json:"existingDR,omitempty"`
}

// ExistingDRInfo holds info about an active DR that already contains a given artifact.
type ExistingDRInfo struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// PreviewDRSummary provides counts per category for the Preview response.
type PreviewDRSummary struct {
	TotalMismatch  int `json:"totalMismatch"`
	Includable     int `json:"includable"`
	Draft          int `json:"draft"`
	VersionPattern int `json:"versionPattern"`
	Duplicate      int `json:"duplicate"`
}

// PreviewDRResponse is the response for the Preview API.
type PreviewDRResponse struct {
	SnapshotID          uint                `json:"snapshotID"`
	SnapshotCompletedAt time.Time           `json:"snapshotCompletedAt"`
	RuleName            string              `json:"ruleName"`
	RequireJira         bool                `json:"requireJira"`
	Artifacts           []PreviewDRArtifact `json:"artifacts"`
	Summary             PreviewDRSummary    `json:"summary"`
}

// ArtifactKey uniquely identifies an artifact within a snapshot (artifactID + packageID),
// and optionally carries per-artifact configuration for DR creation.
type ArtifactKey struct {
	ArtifactID string `json:"artifactID"`
	PackageID  string `json:"packageID"`
	SkipDeploy bool   `json:"skipDeploy"`
}

// CreateDRFromMismatchRequest is the request body for the Create API.
type CreateDRFromMismatchRequest struct {
	Name                string        `json:"name"`
	JiraLink            string        `json:"jiraLink"`
	SnapshotID          uint          `json:"snapshotID"`
	SnapshotCompletedAt time.Time     `json:"snapshotCompletedAt"`
	ArtifactKeys        []ArtifactKey `json:"artifactKeys"`
}

// MismatchSkipError records an artifact that was skipped during Create due to validation failure.
type MismatchSkipError struct {
	ArtifactID string `json:"artifactID"`
	PackageID  string `json:"packageID"`
	Reason     string `json:"reason"`
}

// CreateDRFromMismatchSummary provides counts for the Create response.
type CreateDRFromMismatchSummary struct {
	Requested int                 `json:"requested"`
	Created   int                 `json:"created"`
	Errors    []MismatchSkipError `json:"errors"`
}

// CreateDRFromMismatchResponse is the response for the Create API.
type CreateDRFromMismatchResponse struct {
	DeliveryRequest db.DeliveryRequest          `json:"deliveryRequest"`
	Summary         CreateDRFromMismatchSummary `json:"summary"`
}

// PreviewDRFromMismatch analyzes a completed snapshot and returns categorized DT-mismatch artifacts.
//
// Classification priority (mutually exclusive): DRAFT > VersionPattern > Duplicate > Includable.
func (s *Service) PreviewDRFromMismatch(ruleID uint) (PreviewDRResponse, error) {
	// 1. Load delivery rule with associations
	rule, err := s.GetDeliveryRuleWithAcc(ruleID)
	if err != nil {
		return PreviewDRResponse{}, err
	}

	// 2. Load completed snapshot
	var snapshot db.VersionCompareSnapshot
	if err := s.DB.
		Where(&db.VersionCompareSnapshot{DeliveryRuleID: ruleID, Status: consts.SnapshotStatusCompleted}).
		First(&snapshot).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PreviewDRResponse{}, fmt.Errorf("no completed version compare snapshot found for rule %d", ruleID)
		}
		return PreviewDRResponse{}, fmt.Errorf("failed to query snapshot: %w", err)
	}
	if snapshot.CompletedAt == nil {
		return PreviewDRResponse{}, fmt.Errorf("snapshot %d has no completion timestamp", snapshot.ID)
	}

	// 3. Query active DRs for duplicate detection
	// Active = NOT IN (CANCELED, DEPLOYED)
	var activeDRs []db.DeliveryRequest
	if err := s.DB.
		Where("delivery_rule_id = ? AND aggregate_status NOT IN ?", ruleID,
			[]lifecycle.AggregateStatus{lifecycle.AggCanceled, lifecycle.AggDeployed}).
		Preload("ArtifactTenantOperations").
		Find(&activeDRs).Error; err != nil {
		return PreviewDRResponse{}, fmt.Errorf("failed to query active delivery requests: %w", err)
	}

	// Build existing ops index: (artifactTechID, artifactVersion) -> DR info
	type dupKey struct {
		techID  string
		version string
	}
	existingOps := make(map[dupKey]ExistingDRInfo)
	for _, dr := range activeDRs {
		for _, op := range dr.ArtifactTenantOperations {
			k := dupKey{techID: op.ArtifactTechID, version: op.ArtifactVersion}
			if _, exists := existingOps[k]; !exists {
				existingOps[k] = ExistingDRInfo{ID: dr.ID, Name: dr.Name}
			}
		}
	}

	// 4. Iterate snapshot data, classify DT-mismatch artifacts
	var artifacts []PreviewDRArtifact
	var summary PreviewDRSummary
	sourceTenantID := snapshot.Data.SourceTenantID

	for _, pkg := range snapshot.Data.Packages {
		for _, art := range pkg.Artifacts {
			sourceVI, sourceHasData := art.Versions[sourceTenantID]

			// Skip artifacts where source has no data
			if !sourceHasData || sourceVI.DesignTimeVersion == "" {
				continue
			}

			// Check DT mismatch: any compared tenant differs from source
			hasMismatch := false
			for _, targetID := range snapshot.Data.ComparedTenants {
				targetVI, targetHasData := art.Versions[targetID]
				if !targetHasData || targetVI.DesignTimeVersion != sourceVI.DesignTimeVersion {
					hasMismatch = true
					break
				}
			}
			if !hasMismatch {
				continue
			}

			summary.TotalMismatch++
			pa := PreviewDRArtifact{
				ArtifactID:    art.ID,
				ArtifactName:  art.Name,
				PackageID:     pkg.PackageID,
				Type:          art.Type,
				SourceVersion: sourceVI.DesignTimeVersion,
				ModifiedBy:    sourceVI.ModifiedBy,
				ModifiedAt:    sourceVI.ModifiedAt,
			}

			// Classify (mutually exclusive, priority order)
			if strings.EqualFold(sourceVI.DesignTimeVersion, "active") {
				pa.Category = "draft"
				summary.Draft++
			} else if !matchVersionPattern(sourceVI.DesignTimeVersion, rule.VersionPattern) {
				pa.Category = "versionPattern"
				pa.Reason = fmt.Sprintf("version %s does not match pattern %s", sourceVI.DesignTimeVersion, rule.VersionPattern)
				summary.VersionPattern++
			} else if drInfo, isDup := existingOps[dupKey{techID: art.ID, version: sourceVI.DesignTimeVersion}]; isDup {
				pa.Category = "duplicate"
				pa.Reason = fmt.Sprintf("already in active DR #%d \"%s\"", drInfo.ID, drInfo.Name)
				pa.ExistingDR = &drInfo
				summary.Duplicate++
			} else {
				pa.Category = "includable"
				summary.Includable++
			}

			artifacts = append(artifacts, pa)
		}
	}

	if summary.TotalMismatch == 0 {
		return PreviewDRResponse{}, fmt.Errorf("no design-time mismatches found in snapshot")
	}

	return PreviewDRResponse{
		SnapshotID:          snapshot.ID,
		SnapshotCompletedAt: *snapshot.CompletedAt,
		RuleName:            rule.Name,
		RequireJira:         rule.RequireJira,
		Artifacts:           artifacts,
		Summary:             summary,
	}, nil
}

// CreateDRFromMismatch creates a DR pre-filled with user-selected mismatch artifacts.
//
// It validates snapshot staleness, runs version downgrade checks (with tolerance),
// and only creates the DR if at least one artifact passes all checks.
func (s *Service) CreateDRFromMismatch(ctx context.Context, ruleID uint, req CreateDRFromMismatchRequest, user string) (CreateDRFromMismatchResponse, error) {
	// 1. Load delivery rule
	rule, err := s.GetDeliveryRuleWithAcc(ruleID)
	if err != nil {
		return CreateDRFromMismatchResponse{}, err
	}

	// 2. Load snapshot and validate consistency
	var snapshot db.VersionCompareSnapshot
	if err := s.DB.
		First(&snapshot, req.SnapshotID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CreateDRFromMismatchResponse{}, fmt.Errorf("snapshot %d not found or not completed for rule %d", req.SnapshotID, ruleID)
		}
		return CreateDRFromMismatchResponse{}, fmt.Errorf("failed to query snapshot: %w", err)
	}
	if snapshot.DeliveryRuleID != ruleID || snapshot.Status != consts.SnapshotStatusCompleted {
		return CreateDRFromMismatchResponse{}, fmt.Errorf("snapshot %d not found or not completed for rule %d", req.SnapshotID, ruleID)
	}
	if snapshot.CompletedAt == nil {
		return CreateDRFromMismatchResponse{}, fmt.Errorf("snapshot %d has no completion timestamp", snapshot.ID)
	}
	// Staleness check: compare CompletedAt timestamps
	if !snapshot.CompletedAt.Equal(req.SnapshotCompletedAt) {
		return CreateDRFromMismatchResponse{}, fmt.Errorf("snapshot data has changed since preview (completedAt mismatch): preview saw %s, current is %s",
			req.SnapshotCompletedAt.Format(time.RFC3339), snapshot.CompletedAt.Format(time.RFC3339))
	}

	// 3. Validate artifactKeys non-empty
	if len(req.ArtifactKeys) == 0 {
		return CreateDRFromMismatchResponse{}, fmt.Errorf("artifactKeys must not be empty")
	}

	// 4. JIRA validation
	if rule.RequireJira && req.JiraLink == "" {
		return CreateDRFromMismatchResponse{}, fmt.Errorf("jira link is required for delivery rule \"%s\"", rule.Name)
	}

	// 5. Auto-generate name if not provided
	// Format: "Auto DR - <rule name> - VC <snapshot completion time>"
	if req.Name == "" {
		req.Name = fmt.Sprintf("Auto DR - %s - VC %s", rule.Name, snapshot.CompletedAt.Format("2006-01-02 15:04"))
	}

	// 6. Build artifact key lookup set (keyed by ArtifactID+PackageID only; SkipDeploy is config, not identity)
	type artIdentity struct {
		ArtifactID string
		PackageID  string
	}
	artifactKeySet := make(map[artIdentity]bool, len(req.ArtifactKeys))
	skipDeployMap := make(map[artIdentity]bool, len(req.ArtifactKeys))
	for _, k := range req.ArtifactKeys {
		id := artIdentity{ArtifactID: k.ArtifactID, PackageID: k.PackageID}
		artifactKeySet[id] = true
		skipDeployMap[id] = k.SkipDeploy
	}

	// 7. Find selected artifacts in snapshot
	type artifactWithPkg struct {
		art        db.ArtifactSnapshot
		pkg        string // packageID
		skipDeploy bool
	}
	var selectedArtifacts []artifactWithPkg
	for _, pkg := range snapshot.Data.Packages {
		for _, art := range pkg.Artifacts {
			id := artIdentity{ArtifactID: art.ID, PackageID: pkg.PackageID}
			if artifactKeySet[id] {
				selectedArtifacts = append(selectedArtifacts, artifactWithPkg{art: art, pkg: pkg.PackageID, skipDeploy: skipDeployMap[id]})
			}
		}
	}

	// 8. Build ops with tolerance for individual failures
	var validOps []db.ArtifactTenantOperation
	var skipErrors []MismatchSkipError
	sourceTenantID := snapshot.Data.SourceTenantID

	for _, item := range selectedArtifacts {
		art := item.art
		sourceVI, sourceHasData := art.Versions[sourceTenantID]
		if !sourceHasData || sourceVI.DesignTimeVersion == "" {
			skipErrors = append(skipErrors, MismatchSkipError{
				ArtifactID: art.ID,
				PackageID:  item.pkg,
				Reason:     "source tenant has no data for this artifact",
			})
			continue
		}

		// Build the operation struct
		op := db.ArtifactTenantOperation{
			TenantID:               rule.SourceTenantID,
			ArtifactTechID:         art.ID,
			ArtifactVersion:        sourceVI.DesignTimeVersion,
			ArtifactName:           art.Name,
			ArtifactType:           consts.ArtifactType(art.Type),
			PackageID:              item.pkg,
			TransportRequestNumber: "", // empty — to be filled later by TR generation
			SkipDeploy:             item.skipDeploy,
		}

		// Artifact tech ID comes from the version snapshot (already correct) — no PIR lookup needed.

		validOps = append(validOps, op)
	}

	// 9. If no valid ops, don't create an empty DR
	if len(validOps) == 0 {
		return CreateDRFromMismatchResponse{
			Summary: CreateDRFromMismatchSummary{
				Requested: len(req.ArtifactKeys),
				Created:   0,
				Errors:    skipErrors,
			},
		}, fmt.Errorf("no artifacts passed validation checks")
	}

	// 10. Create DR with VersionCompareSnapshotID
	dr := db.DeliveryRequest{
		Name:                     req.Name,
		JiraLink:                 req.JiraLink,
		DeliveryRuleID:           rule.ID,
		SourceTenantID:           rule.SourceTenantID,
		AggregateStatus:          lifecycle.AggPending,
		VersionCompareSnapshotID: &snapshot.ID,
		CreatedBy:                user,
		UpdatedBy:                user,
	}
	if err := s.CreateDeliveryRequest(&dr); err != nil {
		return CreateDRFromMismatchResponse{}, fmt.Errorf("failed to create delivery request: %w", err)
	}

	// 11. Insert ops via InsertTenantOps — handles lifecycle states, DeliveryRuleCheck (incl. downgrade), and fires TR generation goroutine
	createdOps, err := s.InsertTenantOps(ctx, dr.ID, validOps, user)
	if err != nil {
		return CreateDRFromMismatchResponse{}, fmt.Errorf("failed to create artifact tenant operations: %w", err)
	}

	// 12. Build response from in-memory data — avoids a DB reload that would race with the TR generation goroutine
	var sourceTenant db.CpiTenant
	for _, t := range rule.IncludedTenants {
		if t.ID == rule.SourceTenantID {
			sourceTenant = t
			break
		}
	}
	for i := range createdOps {
		createdOps[i].Tenant = sourceTenant
	}
	dr.ArtifactTenantOperations = createdOps
	dr.SourceTenant = sourceTenant
	dr.DeliveryRule = rule

	return CreateDRFromMismatchResponse{
		DeliveryRequest: dr,
		Summary: CreateDRFromMismatchSummary{
			Requested: len(req.ArtifactKeys),
			Created:   len(createdOps),
			Errors:    skipErrors,
		},
	}, nil
}

// --- Helpers ---

// computeMismatchCounts calculates matched/mismatched/total artifact counts from snapshot data.
// An artifact is "mismatched" if ANY compared tenant's version differs from source (DT or RT).
func computeMismatchCounts(data db.SnapshotData) (matched, mismatched, total int) {
	for _, pkg := range data.Packages {
		for _, art := range pkg.Artifacts {
			total++
			sourceVersion, ok := art.Versions[data.SourceTenantID]
			if !ok {
				continue
			}

			artMismatch := false
			for tenantID, vi := range art.Versions {
				if tenantID == data.SourceTenantID {
					continue
				}
				if vi.DesignTimeVersion != sourceVersion.DesignTimeVersion ||
					vi.RuntimeVersion != sourceVersion.RuntimeVersion {
					artMismatch = true
					break
				}
			}

			if artMismatch {
				mismatched++
			} else {
				matched++
			}
		}
	}
	return
}

// ParsePackageIDs splits a comma-separated string into a slice of package IDs.
func ParsePackageIDs(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
