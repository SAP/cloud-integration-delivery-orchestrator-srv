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

	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

// versionCompareCooldown is the minimum interval between triggers for the same rule.
const versionCompareCooldown = 5 * time.Minute

// TriggerResult is returned immediately by TriggerVersionCompare.
type TriggerResult struct {
	Status consts.TriggerStatus `json:"status"`
}

// TriggerVersionCompare starts an asynchronous version snapshot collection for a Delivery Rule.
//
// Flow:
//  1. Load DeliveryRule with SourceTenant + IncludedTenants.
//  2. Rate-limit check: if a completed snapshot exists within cooldown, return rate_limited.
//  3. Atomic DB upsert: set status="running". If already running → conflict.
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

	// Get CPI client for source tenant
	sourceClient, err := s.CPI(ctx, rule.SourceTenant.CpiEndpoint.Name)
	if err != nil {
		completeWithError(fmt.Sprintf("failed to get source tenant CPI client: %s", err))
		return
	}

	// Get packages from source tenant
	packages, err := sourceClient.GetPackages(ctx)
	if err != nil {
		completeWithError(fmt.Sprintf("failed to get packages from source tenant: %s", err))
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

	// Pre-fetch runtime artifacts for each tenant (one call per tenant)
	// map[tenantID] -> map[artifactID] -> RuntimeArtifact
	runtimeIndex := make(map[uint]map[string]cpi.RuntimeArtifact)
	var rtMu sync.Mutex
	rtGroup, rtCtx := errgroup.WithContext(ctx)

	for _, tenant := range allTenants {
		tenant := tenant // capture loop variable
		rtGroup.Go(func() error {
			client, err := s.CPI(rtCtx, tenant.CpiEndpoint.Name)
			if err != nil {
				s.Logger.Warnf("version-compare: failed to get CPI client for tenant %s (ID=%d): %s",
					tenant.CpiEndpoint.Name, tenant.ID, err)
				// Store empty map, don't fail entire operation
				rtMu.Lock()
				runtimeIndex[tenant.ID] = make(map[string]cpi.RuntimeArtifact)
				rtMu.Unlock()
				return nil
			}
			artifacts, err := client.GetRuntimeArtifacts(rtCtx)
			if err != nil {
				s.Logger.Warnf("version-compare: failed to get runtime artifacts for tenant %s (ID=%d): %s",
					tenant.CpiEndpoint.Name, tenant.ID, err)
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
	if err := rtGroup.Wait(); err != nil {
		completeWithError(fmt.Sprintf("failed to fetch runtime artifacts: %s", err))
		return
	}

	// Fetch design-time artifacts per package per tenant, build PackageSnapshots
	var pkgSnapshots []db.PackageSnapshot
	var pkgMu sync.Mutex
	pkgGroup, pkgCtx := errgroup.WithContext(ctx)
	// Limit concurrency to avoid overwhelming CPI APIs
	pkgGroup.SetLimit(10)

	for _, pkg := range packages {
		pkg := pkg
		pkgGroup.Go(func() error {
			pkgSnapshot, err := s.collectPackageSnapshot(pkgCtx, pkg.ID, allTenants, runtimeIndex)
			if err != nil {
				s.Logger.Warnf("version-compare: failed to collect package %s: %s", pkg.ID, err)
				// Don't fail the whole operation, skip this package
				return nil
			}
			pkgMu.Lock()
			pkgSnapshots = append(pkgSnapshots, pkgSnapshot)
			pkgMu.Unlock()
			return nil
		})
	}
	if err := pkgGroup.Wait(); err != nil {
		completeWithError(fmt.Sprintf("failed to collect package snapshots: %s", err))
		return
	}

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

// collectPackageSnapshot fetches design-time artifacts for one package across all tenants
// and enriches them with runtime info from the pre-fetched index.
func (s *Service) collectPackageSnapshot(
	ctx context.Context,
	packageID string,
	tenants []db.CpiTenant,
	runtimeIndex map[uint]map[string]cpi.RuntimeArtifact,
) (db.PackageSnapshot, error) {

	// For each tenant, fetch design-time artifacts for this package
	type tenantArtifacts struct {
		tenantID  uint
		artifacts []db.Artifact
		err       error
	}

	results := make([]tenantArtifacts, len(tenants))
	g, gctx := errgroup.WithContext(ctx)

	for i, tenant := range tenants {
		i, tenant := i, tenant
		g.Go(func() error {
			client, err := s.CPI(gctx, tenant.CpiEndpoint.Name)
			if err != nil {
				results[i] = tenantArtifacts{tenantID: tenant.ID, err: err}
				return nil // error tolerance: don't fail entire group
			}
			arts, err := FetchPackageArtifacts(gctx, client, packageID)
			results[i] = tenantArtifacts{tenantID: tenant.ID, artifacts: arts, err: err}
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
					Type:     string(art.Type),
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
			s.Logger.Warnf("version-compare: tenant %d error for package %s: %s",
				result.tenantID, packageID, result.err)
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
	tenantMap := make(map[uint]db.CpiTenant, len(tenants))
	for _, t := range tenants {
		tenantMap[t.ID] = t
	}

	// Build tenant info list
	for _, id := range tenantIDs {
		if t, ok := tenantMap[id]; ok {
			resp.Tenants = append(resp.Tenants, VersionCompareTenantInfo{
				ID:       t.ID,
				Name:     t.Name,
				IsSource: t.ID == snapshot.Data.SourceTenantID,
			})
		}
	}

	// Build packageID filter set
	pkgFilter := make(map[string]bool)
	for _, id := range params.PackageIDs {
		pkgFilter[id] = true
	}

	// Process packages with match computation
	for _, pkg := range snapshot.Data.Packages {
		// Apply package filter
		if len(pkgFilter) > 0 && !pkgFilter[pkg.PackageID] {
			continue
		}

		var filteredArtifacts []VersionCompareArtifact
		for _, art := range pkg.Artifacts {
			sourceVersion, sourceHasData := art.Versions[snapshot.Data.SourceTenantID]
			hasMismatch := false

			versions := make(map[uint]VersionCompareArtifactTenantInfo, len(art.Versions))
			for tenantID, vi := range art.Versions {
				info := VersionCompareArtifactTenantInfo{
					Error: vi.Error,
				}

				if params.DesignTime {
					info.DesignTimeVersion = vi.DesignTimeVersion
					info.DesignTimeDraft = vi.DesignTimeVersion == "active"
					if tenantID == snapshot.Data.SourceTenantID {
						info.ModifiedBy = vi.ModifiedBy
						info.ModifiedAt = vi.ModifiedAt
					}
					if tenantID != snapshot.Data.SourceTenantID && sourceHasData {
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
					if tenantID != snapshot.Data.SourceTenantID && sourceHasData {
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

	return resp, nil
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
		// Delete all existing entries
		if err := tx.Where("1 = 1").Delete(&db.VersionCompareIncludedPackage{}).Error; err != nil {
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
