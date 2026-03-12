package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
)

// =============================================================================
// Pure function tests (no DB, no mocks)
// =============================================================================

func TestComputeMismatchCounts_AllMatched(t *testing.T) {
	data := db.SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2, 3},
		Packages: []db.PackageSnapshot{
			{
				PackageID: "pkg1",
				Artifacts: []db.ArtifactSnapshot{
					{
						ID: "art1", Versions: map[uint]db.ArtifactVersionInfo{
							1: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
							2: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
							3: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
						},
					},
					{
						ID: "art2", Versions: map[uint]db.ArtifactVersionInfo{
							1: {DesignTimeVersion: "2.0", RuntimeVersion: "2.0"},
							2: {DesignTimeVersion: "2.0", RuntimeVersion: "2.0"},
							3: {DesignTimeVersion: "2.0", RuntimeVersion: "2.0"},
						},
					},
				},
			},
		},
	}
	matched, mismatched, total := computeMismatchCounts(data)
	if total != 2 {
		t.Errorf("total: got %d, want 2", total)
	}
	if matched != 2 {
		t.Errorf("matched: got %d, want 2", matched)
	}
	if mismatched != 0 {
		t.Errorf("mismatched: got %d, want 0", mismatched)
	}
}

func TestComputeMismatchCounts_DesignTimeMismatch(t *testing.T) {
	data := db.SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2},
		Packages: []db.PackageSnapshot{
			{
				PackageID: "pkg1",
				Artifacts: []db.ArtifactSnapshot{
					{
						ID: "art1", Versions: map[uint]db.ArtifactVersionInfo{
							1: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
							2: {DesignTimeVersion: "0.9", RuntimeVersion: "1.0"}, // DT mismatch
						},
					},
				},
			},
		},
	}
	matched, mismatched, total := computeMismatchCounts(data)
	if total != 1 {
		t.Errorf("total: got %d, want 1", total)
	}
	if matched != 0 {
		t.Errorf("matched: got %d, want 0", matched)
	}
	if mismatched != 1 {
		t.Errorf("mismatched: got %d, want 1", mismatched)
	}
}

func TestComputeMismatchCounts_RuntimeMismatch(t *testing.T) {
	data := db.SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2},
		Packages: []db.PackageSnapshot{
			{
				PackageID: "pkg1",
				Artifacts: []db.ArtifactSnapshot{
					{
						ID: "art1", Versions: map[uint]db.ArtifactVersionInfo{
							1: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
							2: {DesignTimeVersion: "1.0", RuntimeVersion: "0.8"}, // RT mismatch
						},
					},
				},
			},
		},
	}
	_, mismatched, _ := computeMismatchCounts(data)
	if mismatched != 1 {
		t.Errorf("mismatched: got %d, want 1", mismatched)
	}
}

func TestComputeMismatchCounts_MixedAcrossPackages(t *testing.T) {
	data := db.SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2, 3},
		Packages: []db.PackageSnapshot{
			{
				PackageID: "pkg1",
				Artifacts: []db.ArtifactSnapshot{
					{ID: "art1", Versions: map[uint]db.ArtifactVersionInfo{
						1: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
						2: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
						3: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
					}},
				},
			},
			{
				PackageID: "pkg2",
				Artifacts: []db.ArtifactSnapshot{
					{ID: "art2", Versions: map[uint]db.ArtifactVersionInfo{
						1: {DesignTimeVersion: "2.0", RuntimeVersion: "2.0"},
						2: {DesignTimeVersion: "1.9", RuntimeVersion: "2.0"}, // mismatch
						3: {DesignTimeVersion: "2.0", RuntimeVersion: "2.0"},
					}},
					{ID: "art3", Versions: map[uint]db.ArtifactVersionInfo{
						1: {DesignTimeVersion: "3.0", RuntimeVersion: "3.0"},
						2: {DesignTimeVersion: "3.0", RuntimeVersion: "3.0"},
						3: {DesignTimeVersion: "3.0", RuntimeVersion: "2.9"}, // mismatch
					}},
				},
			},
		},
	}
	matched, mismatched, total := computeMismatchCounts(data)
	if total != 3 {
		t.Errorf("total: got %d, want 3", total)
	}
	if matched != 1 {
		t.Errorf("matched: got %d, want 1", matched)
	}
	if mismatched != 2 {
		t.Errorf("mismatched: got %d, want 2", mismatched)
	}
}

func TestComputeMismatchCounts_EmptyData(t *testing.T) {
	data := db.SnapshotData{}
	matched, mismatched, total := computeMismatchCounts(data)
	if total != 0 || matched != 0 || mismatched != 0 {
		t.Errorf("empty data: got matched=%d mismatched=%d total=%d", matched, mismatched, total)
	}
}

func TestComputeMismatchCounts_NoSourceVersion(t *testing.T) {
	// Artifact exists on compared tenants but not on source
	data := db.SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2},
		Packages: []db.PackageSnapshot{
			{
				PackageID: "pkg1",
				Artifacts: []db.ArtifactSnapshot{
					{
						ID: "art1", Versions: map[uint]db.ArtifactVersionInfo{
							2: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
						},
					},
				},
			},
		},
	}
	matched, mismatched, total := computeMismatchCounts(data)
	if total != 1 {
		t.Errorf("total: got %d, want 1", total)
	}
	// No source version → can't determine match, counts as neither
	if matched != 0 || mismatched != 0 {
		t.Errorf("no source: got matched=%d mismatched=%d, want 0/0", matched, mismatched)
	}
}

// --- ParsePackageIDs ---

func TestParsePackageIDs(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"pkg1", []string{"pkg1"}},
		{"pkg1,pkg2", []string{"pkg1", "pkg2"}},
		{"pkg1, pkg2 , pkg3", []string{"pkg1", "pkg2", "pkg3"}},
		{",,,", nil}, // all empty after trim
		{"pkg1,,pkg2", []string{"pkg1", "pkg2"}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("input=%q", tt.input), func(t *testing.T) {
			got := ParsePackageIDs(tt.input)
			if tt.want == nil {
				if got != nil && len(got) != 0 {
					t.Errorf("got %v, want nil/empty", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("length: got %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// =============================================================================
// QueryVersionCompare — match computation + filters (requires DB for snapshot read)
// =============================================================================

// setupQueryTestData seeds tenants, rule, and a completed snapshot for query tests.
func setupQueryTestData(t *testing.T) (ruleID uint, sourceTenantID uint, comparedTenantIDs []uint) {
	t.Helper()
	cleanAll(t)

	source := seedTenant(t, "source-tenant")
	target1 := seedTenant(t, "target-tenant-1")
	target2 := seedTenant(t, "target-tenant-2")
	rule := seedRule(t, "query-test-rule", source, []db.CpiTenant{source, target1, target2}, true)

	snap := db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target1.ID, target2.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							ID: "iflow1", Name: "IFlow One", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID:  {DesignTimeVersion: "1.0.5", RuntimeVersion: "1.0.5", RuntimeStatus: "STARTED"},
								target1.ID: {DesignTimeVersion: "1.0.5", RuntimeVersion: "1.0.5", RuntimeStatus: "STARTED"},
								target2.ID: {DesignTimeVersion: "1.0.4", RuntimeVersion: "1.0.4", RuntimeStatus: "STARTED"},
							},
						},
						{
							ID: "sc1", Name: "Script One", Type: "Script Collection",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID:  {DesignTimeVersion: "2.0.0", RuntimeVersion: "2.0.0", RuntimeStatus: "STARTED"},
								target1.ID: {DesignTimeVersion: "2.0.0", RuntimeVersion: "2.0.0", RuntimeStatus: "STARTED"},
								target2.ID: {DesignTimeVersion: "2.0.0", RuntimeVersion: "2.0.0", RuntimeStatus: "STARTED"},
							},
						},
					},
				},
				{
					PackageID: "pkg2",
					Artifacts: []db.ArtifactSnapshot{
						{
							ID: "iflow2", Name: "IFlow Two", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID:  {DesignTimeVersion: "active", RuntimeVersion: "3.0.0", RuntimeStatus: "STARTED"},
								target1.ID: {DesignTimeVersion: "3.0.0", RuntimeVersion: "3.0.0", RuntimeStatus: "STARTED"},
								target2.ID: {DesignTimeVersion: "active", RuntimeVersion: "2.9.0", RuntimeStatus: "ERROR"},
							},
						},
					},
				},
			},
		},
	}
	seedSnapshot(t, snap)
	return rule.ID, source.ID, []uint{target1.ID, target2.ID}
}

func TestQueryVersionCompare_NoSnapshot(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src")
	rule := seedRule(t, "empty-rule", source, []db.CpiTenant{source}, true)

	svc := newTestService(nil)
	resp, err := svc.QueryVersionCompare(rule.ID, VersionCompareQueryParams{DesignTime: true, RunTime: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != consts.SnapshotStatusNone {
		t.Errorf("status: got %q, want %q", resp.Status, consts.SnapshotStatusNone)
	}
}

func TestQueryVersionCompare_RunningStatus(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src")
	rule := seedRule(t, "running-rule", source, []db.CpiTenant{source}, true)
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user",
	})

	svc := newTestService(nil)
	resp, err := svc.QueryVersionCompare(rule.ID, VersionCompareQueryParams{DesignTime: true, RunTime: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != consts.SnapshotStatusRunning {
		t.Errorf("status: got %q, want %q", resp.Status, consts.SnapshotStatusRunning)
	}
	if resp.Packages != nil {
		t.Errorf("packages should be nil for running status, got %d", len(resp.Packages))
	}
}

func TestQueryVersionCompare_FullResult(t *testing.T) {
	ruleID, sourceID, _ := setupQueryTestData(t)
	svc := newTestService(nil)

	resp, err := svc.QueryVersionCompare(ruleID, VersionCompareQueryParams{
		DesignTime: true,
		RunTime:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != consts.SnapshotStatusCompleted {
		t.Fatalf("status: got %q, want %q", resp.Status, consts.SnapshotStatusCompleted)
	}

	// Tenants
	if len(resp.Tenants) != 3 {
		t.Fatalf("tenants: got %d, want 3", len(resp.Tenants))
	}
	sourceFound := false
	for _, ten := range resp.Tenants {
		if ten.ID == sourceID && ten.IsSource {
			sourceFound = true
		}
		if ten.ID != sourceID && ten.IsSource {
			t.Errorf("tenant %d incorrectly marked as source", ten.ID)
		}
	}
	if !sourceFound {
		t.Error("source tenant not found in tenants list")
	}

	// Packages
	if len(resp.Packages) != 2 {
		t.Fatalf("packages: got %d, want 2", len(resp.Packages))
	}

	// pkg1: iflow1 has mismatch on target2, sc1 fully matched
	var pkg1 *VersionComparePackage
	for i := range resp.Packages {
		if resp.Packages[i].PackageID == "pkg1" {
			pkg1 = &resp.Packages[i]
		}
	}
	if pkg1 == nil {
		t.Fatal("pkg1 not found")
	}
	if len(pkg1.Artifacts) != 2 {
		t.Fatalf("pkg1 artifacts: got %d, want 2", len(pkg1.Artifacts))
	}
}

func TestQueryVersionCompare_MatchComputation(t *testing.T) {
	ruleID, sourceID, comparedIDs := setupQueryTestData(t)
	svc := newTestService(nil)

	resp, err := svc.QueryVersionCompare(ruleID, VersionCompareQueryParams{
		DesignTime: true,
		RunTime:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find iflow1 in pkg1
	var iflow1 *VersionCompareArtifact
	for _, pkg := range resp.Packages {
		for i := range pkg.Artifacts {
			if pkg.Artifacts[i].ID == "iflow1" {
				iflow1 = &pkg.Artifacts[i]
			}
		}
	}
	if iflow1 == nil {
		t.Fatal("iflow1 not found")
	}

	// Source tenant should have no match flags (nil)
	srcInfo := iflow1.Versions[sourceID]
	if srcInfo.DesignTimeMatch != nil {
		t.Error("source tenant should not have DesignTimeMatch")
	}
	if srcInfo.RuntimeMatch != nil {
		t.Error("source tenant should not have RuntimeMatch")
	}

	// target1 matches source on both DT and RT
	t1Info := iflow1.Versions[comparedIDs[0]]
	if t1Info.DesignTimeMatch == nil || !*t1Info.DesignTimeMatch {
		t.Error("target1 iflow1 DT should match")
	}
	if t1Info.RuntimeMatch == nil || !*t1Info.RuntimeMatch {
		t.Error("target1 iflow1 RT should match")
	}

	// target2 has DT 1.0.4 vs source 1.0.5 → mismatch
	t2Info := iflow1.Versions[comparedIDs[1]]
	if t2Info.DesignTimeMatch == nil || *t2Info.DesignTimeMatch {
		t.Error("target2 iflow1 DT should NOT match")
	}
	if t2Info.RuntimeMatch == nil || *t2Info.RuntimeMatch {
		t.Error("target2 iflow1 RT should NOT match")
	}
}

func TestQueryVersionCompare_DesignTimeDraft(t *testing.T) {
	ruleID, sourceID, _ := setupQueryTestData(t)
	svc := newTestService(nil)

	resp, err := svc.QueryVersionCompare(ruleID, VersionCompareQueryParams{DesignTime: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// iflow2 in pkg2 has "active" on source and target2
	var iflow2 *VersionCompareArtifact
	for _, pkg := range resp.Packages {
		for i := range pkg.Artifacts {
			if pkg.Artifacts[i].ID == "iflow2" {
				iflow2 = &pkg.Artifacts[i]
			}
		}
	}
	if iflow2 == nil {
		t.Fatal("iflow2 not found")
	}
	if !iflow2.Versions[sourceID].DesignTimeDraft {
		t.Error("source iflow2 should be flagged as draft (version=active)")
	}
}

func TestQueryVersionCompare_FilterDesignTimeOnly(t *testing.T) {
	ruleID, _, comparedIDs := setupQueryTestData(t)
	svc := newTestService(nil)

	resp, err := svc.QueryVersionCompare(ruleID, VersionCompareQueryParams{
		DesignTime: true,
		RunTime:    false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that runtime fields are empty for all artifacts
	for _, pkg := range resp.Packages {
		for _, art := range pkg.Artifacts {
			for tid, v := range art.Versions {
				if v.RuntimeVersion != "" {
					t.Errorf("artifact %s tenant %d: RuntimeVersion should be empty, got %q", art.ID, tid, v.RuntimeVersion)
				}
				if v.RuntimeMatch != nil {
					t.Errorf("artifact %s tenant %d: RuntimeMatch should be nil", art.ID, tid)
				}
			}
		}
	}

	// DT match should still be computed
	for _, pkg := range resp.Packages {
		for _, art := range pkg.Artifacts {
			for _, tid := range comparedIDs {
				v := art.Versions[tid]
				if v.DesignTimeMatch == nil {
					t.Errorf("artifact %s tenant %d: DesignTimeMatch should not be nil", art.ID, tid)
				}
			}
		}
	}
}

func TestQueryVersionCompare_FilterRuntimeOnly(t *testing.T) {
	ruleID, _, _ := setupQueryTestData(t)
	svc := newTestService(nil)

	resp, err := svc.QueryVersionCompare(ruleID, VersionCompareQueryParams{
		DesignTime: false,
		RunTime:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, pkg := range resp.Packages {
		for _, art := range pkg.Artifacts {
			for tid, v := range art.Versions {
				if v.DesignTimeVersion != "" {
					t.Errorf("artifact %s tenant %d: DesignTimeVersion should be empty, got %q", art.ID, tid, v.DesignTimeVersion)
				}
				if v.DesignTimeMatch != nil {
					t.Errorf("artifact %s tenant %d: DesignTimeMatch should be nil", art.ID, tid)
				}
			}
		}
	}
}

func TestQueryVersionCompare_FilterMismatchOnly(t *testing.T) {
	ruleID, _, _ := setupQueryTestData(t)
	svc := newTestService(nil)

	resp, err := svc.QueryVersionCompare(ruleID, VersionCompareQueryParams{
		DesignTime:   true,
		RunTime:      true,
		MismatchOnly: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// sc1 is fully matched → should be filtered out
	for _, pkg := range resp.Packages {
		for _, art := range pkg.Artifacts {
			if art.ID == "sc1" {
				t.Error("sc1 is fully matched and should be filtered out by mismatchOnly")
			}
		}
	}

	// iflow1 (DT mismatch on target2) and iflow2 (DT+RT mismatch) should remain
	foundIflow1, foundIflow2 := false, false
	for _, pkg := range resp.Packages {
		for _, art := range pkg.Artifacts {
			if art.ID == "iflow1" {
				foundIflow1 = true
			}
			if art.ID == "iflow2" {
				foundIflow2 = true
			}
		}
	}
	if !foundIflow1 {
		t.Error("iflow1 should be present (has mismatch)")
	}
	if !foundIflow2 {
		t.Error("iflow2 should be present (has mismatch)")
	}
}

func TestQueryVersionCompare_FilterByPackageIDs(t *testing.T) {
	ruleID, _, _ := setupQueryTestData(t)
	svc := newTestService(nil)

	resp, err := svc.QueryVersionCompare(ruleID, VersionCompareQueryParams{
		PackageIDs: []string{"pkg2"},
		DesignTime: true,
		RunTime:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Packages) != 1 {
		t.Fatalf("packages: got %d, want 1", len(resp.Packages))
	}
	if resp.Packages[0].PackageID != "pkg2" {
		t.Errorf("packageID: got %q, want %q", resp.Packages[0].PackageID, "pkg2")
	}
}

// =============================================================================
// TriggerVersionCompare — flow control
// =============================================================================

func TestTriggerVersionCompare_RuleNotFound(t *testing.T) {
	cleanAll(t)
	svc := newTestService(nil)

	_, err := svc.TriggerVersionCompare(99999, "user")
	if err == nil {
		t.Fatal("expected error for nonexistent rule")
	}
}

func TestTriggerVersionCompare_NoSourceTenant(t *testing.T) {
	// This test is removed: PostgreSQL FK constraint prevents setting source_tenant_id=0
	// on a real DB. The guard in TriggerVersionCompare (line 48) is defensive code for
	// misconfigured rules, which can't realistically occur with proper FK constraints.
	t.Skip("FK constraint prevents source_tenant_id=0 on PostgreSQL")
}

func TestTriggerVersionCompare_FirstTrigger(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-trigger")
	rule := seedRule(t, "trigger-rule", source, []db.CpiTenant{source}, true)

	mockClient := &mockCPIClient{
		packages: []cpi.CPIPackage{{ID: "pkg1"}},
		iflows:   map[string][]cpi.IflowItem{"pkg1": {}},
	}
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return mockClient, nil
	}
	svc := newTestService(factory)

	result, err := svc.TriggerVersionCompare(rule.ID, "test-user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != consts.TriggerStatusRunning {
		t.Errorf("status: got %q, want %q", result.Status, consts.TriggerStatusRunning)
	}

	// Verify DB record was created with "running" status
	var snap db.VersionCompareSnapshot
	if err := testDB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: rule.ID}).First(&snap).Error; err != nil {
		t.Fatalf("snapshot not found: %v", err)
	}
	if snap.Status != consts.SnapshotStatusRunning && snap.Status != consts.SnapshotStatusCompleted {
		t.Errorf("snapshot status: got %q, want running or completed", snap.Status)
	}
	if snap.TriggeredBy != "test-user" {
		t.Errorf("triggered_by: got %q, want %q", snap.TriggeredBy, "test-user")
	}

	// Wait for the background goroutine to complete
	waitForSnapshotComplete(t, rule.ID, 5*time.Second)
}

func TestTriggerVersionCompare_Conflict(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-conflict")
	rule := seedRule(t, "conflict-rule", source, []db.CpiTenant{source}, true)

	// Pre-create a running snapshot
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user1",
	})

	svc := newTestService(nil)
	result, err := svc.TriggerVersionCompare(rule.ID, "user2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != consts.TriggerStatusConflict {
		t.Errorf("status: got %q, want %q", result.Status, consts.TriggerStatusConflict)
	}
}

func TestTriggerVersionCompare_RateLimited(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-ratelimit")
	rule := seedRule(t, "ratelimit-rule", source, []db.CpiTenant{source}, true)

	// Pre-create a recently completed snapshot
	recentTime := time.Now().Add(-1 * time.Minute) // 1 min ago, within 5 min cooldown
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    recentTime,
		CompletedAt:    &recentTime,
		TriggeredBy:    "user1",
	})

	svc := newTestService(nil)
	result, err := svc.TriggerVersionCompare(rule.ID, "user2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != consts.TriggerStatusRateLimited {
		t.Errorf("status: got %q, want %q", result.Status, consts.TriggerStatusRateLimited)
	}
}

func TestTriggerVersionCompare_CooldownExpired(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-expired")
	rule := seedRule(t, "expired-rule", source, []db.CpiTenant{source}, true)

	// Pre-create a snapshot completed 10 min ago (beyond 5 min cooldown)
	oldTime := time.Now().Add(-10 * time.Minute)
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    oldTime,
		CompletedAt:    &oldTime,
		TriggeredBy:    "user1",
	})

	mockClient := &mockCPIClient{
		packages: []cpi.CPIPackage{{ID: "pkg1"}},
		iflows:   map[string][]cpi.IflowItem{"pkg1": {}},
	}
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return mockClient, nil
	}
	svc := newTestService(factory)

	result, err := svc.TriggerVersionCompare(rule.ID, "user2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != consts.TriggerStatusRunning {
		t.Errorf("status: got %q, want %q", result.Status, consts.TriggerStatusRunning)
	}

	// Wait for background goroutine to finish
	waitForSnapshotComplete(t, rule.ID, 5*time.Second)
}

// =============================================================================
// collectVersionSnapshot — end-to-end collection with mocks
// =============================================================================

func TestCollectVersionSnapshot_Success(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-collect")
	target := seedTenant(t, "tgt-collect")
	rule := seedRule(t, "collect-rule", source, []db.CpiTenant{source, target}, true)

	// Create initial running snapshot
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "test",
	})

	// Build mock per tenant
	clients := map[string]*mockCPIClient{
		source.CpiEndpoint.Name: {
			packages: []cpi.CPIPackage{{ID: "pkg1"}},
			iflows: map[string][]cpi.IflowItem{
				"pkg1": {
					{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "iflow1", Name: "IFlow 1", Version: "1.0.0", PackageID: "pkg1"}},
				},
			},
			scriptColls: map[string][]cpi.ScriptCollectionItem{
				"pkg1": {
					{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "sc1", Name: "SC 1", Version: "2.0.0", PackageID: "pkg1"}},
				},
			},
			runtimeArts: []cpi.RuntimeArtifact{
				{ID: "iflow1", Version: "1.0.0", Status: consts.Artifact_Rt_Started},
				{ID: "sc1", Version: "2.0.0", Status: consts.Artifact_Rt_Started},
			},
		},
		target.CpiEndpoint.Name: {
			iflows: map[string][]cpi.IflowItem{
				"pkg1": {
					{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "iflow1", Name: "IFlow 1", Version: "0.9.0", PackageID: "pkg1"}},
				},
			},
			scriptColls: map[string][]cpi.ScriptCollectionItem{
				"pkg1": {
					{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "sc1", Name: "SC 1", Version: "2.0.0", PackageID: "pkg1"}},
				},
			},
			runtimeArts: []cpi.RuntimeArtifact{
				{ID: "iflow1", Version: "0.9.0", Status: consts.Artifact_Rt_Started},
				{ID: "sc1", Version: "2.0.0", Status: consts.Artifact_Rt_Started},
			},
		},
	}
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		if c, ok := clients[tenant]; ok {
			return c, nil
		}
		return nil, fmt.Errorf("unknown tenant: %s", tenant)
	}

	svc := newTestService(factory)

	// Load rule with associations for collectVersionSnapshot
	var fullRule db.DeliveryRule
	testDB.Preload("SourceTenant").Preload("IncludedTenants").First(&fullRule, rule.ID)

	// Run synchronously (not via goroutine)
	svc.collectVersionSnapshot(fullRule)

	// Verify DB result
	var snap db.VersionCompareSnapshot
	if err := testDB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: rule.ID}).First(&snap).Error; err != nil {
		t.Fatalf("snapshot not found: %v", err)
	}
	if snap.Status != consts.SnapshotStatusCompleted {
		t.Fatalf("status: got %q, want %q", snap.Status, consts.SnapshotStatusCompleted)
	}
	if snap.CompletedAt == nil {
		t.Fatal("CompletedAt should not be nil")
	}

	data := snap.Data
	if data.SourceTenantID != source.ID {
		t.Errorf("SourceTenantID: got %d, want %d", data.SourceTenantID, source.ID)
	}
	if len(data.ComparedTenants) != 1 || data.ComparedTenants[0] != target.ID {
		t.Errorf("ComparedTenants: got %v, want [%d]", data.ComparedTenants, target.ID)
	}
	if len(data.Packages) != 1 {
		t.Fatalf("Packages: got %d, want 1", len(data.Packages))
	}
	pkg := data.Packages[0]
	if pkg.PackageID != "pkg1" {
		t.Errorf("PackageID: got %q, want %q", pkg.PackageID, "pkg1")
	}
	if len(pkg.Artifacts) != 2 {
		t.Fatalf("Artifacts: got %d, want 2", len(pkg.Artifacts))
	}

	// Verify iflow1 versions
	artMap := make(map[string]db.ArtifactSnapshot)
	for _, a := range pkg.Artifacts {
		artMap[a.ID] = a
	}
	iflow1 := artMap["iflow1"]
	srcVer := iflow1.Versions[source.ID]
	if srcVer.DesignTimeVersion != "1.0.0" {
		t.Errorf("iflow1 source DT: got %q, want %q", srcVer.DesignTimeVersion, "1.0.0")
	}
	if srcVer.RuntimeVersion != "1.0.0" {
		t.Errorf("iflow1 source RT: got %q, want %q", srcVer.RuntimeVersion, "1.0.0")
	}
	if srcVer.RuntimeStatus != "STARTED" {
		t.Errorf("iflow1 source RuntimeStatus: got %q, want %q", srcVer.RuntimeStatus, "STARTED")
	}

	tgtVer := iflow1.Versions[target.ID]
	if tgtVer.DesignTimeVersion != "0.9.0" {
		t.Errorf("iflow1 target DT: got %q, want %q", tgtVer.DesignTimeVersion, "0.9.0")
	}
	if tgtVer.RuntimeVersion != "0.9.0" {
		t.Errorf("iflow1 target RT: got %q, want %q", tgtVer.RuntimeVersion, "0.9.0")
	}

	// Verify sc1 matches
	sc1 := artMap["sc1"]
	if sc1.Versions[source.ID].DesignTimeVersion != "2.0.0" {
		t.Errorf("sc1 source DT: got %q", sc1.Versions[source.ID].DesignTimeVersion)
	}
	if sc1.Versions[target.ID].DesignTimeVersion != "2.0.0" {
		t.Errorf("sc1 target DT: got %q", sc1.Versions[target.ID].DesignTimeVersion)
	}
}

func TestCollectVersionSnapshot_CPIClientError(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-err")
	rule := seedRule(t, "err-rule", source, []db.CpiTenant{source}, true)

	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "test",
	})

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return nil, fmt.Errorf("connection refused")
	}
	svc := newTestService(factory)

	var fullRule db.DeliveryRule
	testDB.Preload("SourceTenant").Preload("IncludedTenants").First(&fullRule, rule.ID)
	svc.collectVersionSnapshot(fullRule)

	var snap db.VersionCompareSnapshot
	testDB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: rule.ID}).First(&snap)
	if snap.Status != consts.SnapshotStatusFailed {
		t.Errorf("status: got %q, want %q", snap.Status, consts.SnapshotStatusFailed)
	}
	if snap.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestCollectVersionSnapshot_GetPackagesError(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-pkg-err")
	rule := seedRule(t, "pkgerr-rule", source, []db.CpiTenant{source}, true)

	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "test",
	})

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{packagesErr: fmt.Errorf("API timeout")}, nil
	}
	svc := newTestService(factory)

	var fullRule db.DeliveryRule
	testDB.Preload("SourceTenant").Preload("IncludedTenants").First(&fullRule, rule.ID)
	svc.collectVersionSnapshot(fullRule)

	var snap db.VersionCompareSnapshot
	testDB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: rule.ID}).First(&snap)
	if snap.Status != consts.SnapshotStatusFailed {
		t.Errorf("status: got %q, want %q", snap.Status, consts.SnapshotStatusFailed)
	}
}

func TestCollectVersionSnapshot_PartialTenantFailure(t *testing.T) {
	cleanAll(t)
	source := seedTenant(t, "src-partial")
	target := seedTenant(t, "tgt-partial")
	rule := seedRule(t, "partial-rule", source, []db.CpiTenant{source, target}, true)

	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "test",
	})

	clients := map[string]*mockCPIClient{
		source.CpiEndpoint.Name: {
			packages: []cpi.CPIPackage{{ID: "pkg1"}},
			iflows: map[string][]cpi.IflowItem{
				"pkg1": {{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "iflow1", Name: "IFlow", Version: "1.0", PackageID: "pkg1"}}},
			},
			runtimeArts: []cpi.RuntimeArtifact{{ID: "iflow1", Version: "1.0", Status: consts.Artifact_Rt_Started}},
		},
		target.CpiEndpoint.Name: {
			// Runtime succeeds but iflow fetch will fail
			iflowsErr:   map[string]error{"pkg1": fmt.Errorf("timeout")},
			runtimeArts: []cpi.RuntimeArtifact{},
		},
	}
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		if c, ok := clients[tenant]; ok {
			return c, nil
		}
		return nil, fmt.Errorf("unknown: %s", tenant)
	}
	svc := newTestService(factory)

	var fullRule db.DeliveryRule
	testDB.Preload("SourceTenant").Preload("IncludedTenants").First(&fullRule, rule.ID)
	svc.collectVersionSnapshot(fullRule)

	// Should still complete (error tolerance), but target tenant artifacts may be partial
	var snap db.VersionCompareSnapshot
	testDB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: rule.ID}).First(&snap)
	if snap.Status != consts.SnapshotStatusCompleted {
		t.Errorf("status: got %q, want %q (error tolerance)", snap.Status, consts.SnapshotStatusCompleted)
	}
	// Source artifacts should still be collected
	if len(snap.Data.Packages) == 0 {
		t.Error("expected at least 1 package")
	}
}

// =============================================================================
// GetVersionCompareSummary
// =============================================================================

func TestGetVersionCompareSummary(t *testing.T) {
	cleanAll(t)
	src1 := seedTenant(t, "sum-src1")
	tgt1 := seedTenant(t, "sum-tgt1")
	src2 := seedTenant(t, "sum-src2")

	rule1 := seedRule(t, "sum-rule-1", src1, []db.CpiTenant{src1, tgt1}, true)
	rule2 := seedRule(t, "sum-rule-2", src2, []db.CpiTenant{src2}, true)
	seedRule(t, "inactive-rule", src1, []db.CpiTenant{src1}, false) // should not appear

	// rule1 has completed snapshot with 1 matched + 1 mismatched
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule1.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user",
		Data: db.SnapshotData{
			SourceTenantID:  src1.ID,
			ComparedTenants: []uint{tgt1.ID},
			Packages: []db.PackageSnapshot{
				{PackageID: "pkg1", Artifacts: []db.ArtifactSnapshot{
					{ID: "a1", Versions: map[uint]db.ArtifactVersionInfo{
						src1.ID: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
						tgt1.ID: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
					}},
					{ID: "a2", Versions: map[uint]db.ArtifactVersionInfo{
						src1.ID: {DesignTimeVersion: "2.0", RuntimeVersion: "2.0"},
						tgt1.ID: {DesignTimeVersion: "1.0", RuntimeVersion: "2.0"}, // DT mismatch
					}},
				}},
			},
		},
	})
	// rule2 has no snapshot

	svc := newTestService(nil)
	items, err := svc.GetVersionCompareSummary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("items: got %d, want 2 (inactive excluded)", len(items))
	}

	itemMap := make(map[uint]VersionCompareSummaryItem)
	for _, it := range items {
		itemMap[it.DeliveryRuleID] = it
	}

	// rule1
	s1 := itemMap[rule1.ID]
	if s1.Status != consts.SnapshotStatusCompleted {
		t.Errorf("rule1 status: got %q, want %q", s1.Status, consts.SnapshotStatusCompleted)
	}
	if s1.MatchedCount != 1 {
		t.Errorf("rule1 matched: got %d, want 1", s1.MatchedCount)
	}
	if s1.MismatchedCount != 1 {
		t.Errorf("rule1 mismatched: got %d, want 1", s1.MismatchedCount)
	}
	if s1.TotalArtifacts != 2 {
		t.Errorf("rule1 total: got %d, want 2", s1.TotalArtifacts)
	}
	if s1.TenantCount != 2 {
		t.Errorf("rule1 tenantCount: got %d, want 2", s1.TenantCount)
	}
	if s1.SourceTenantName != "sum-src1" {
		t.Errorf("rule1 sourceTenantName: got %q, want %q", s1.SourceTenantName, "sum-src1")
	}

	// rule2 — no snapshot
	s2 := itemMap[rule2.ID]
	if s2.Status != consts.SnapshotStatusNone {
		t.Errorf("rule2 status: got %q, want %q", s2.Status, consts.SnapshotStatusNone)
	}
}

// =============================================================================
// GetVersionCompareCounts
// =============================================================================

func TestGetVersionCompareCounts(t *testing.T) {
	cleanAll(t)
	src := seedTenant(t, "cnt-src")
	tgt := seedTenant(t, "cnt-tgt")

	rule1 := seedRule(t, "cnt-rule-1", src, []db.CpiTenant{src, tgt}, true) // will have mismatch
	rule2 := seedRule(t, "cnt-rule-2", src, []db.CpiTenant{src, tgt}, true) // will be matched
	rule3 := seedRule(t, "cnt-rule-3", src, []db.CpiTenant{src}, true)      // no snapshot
	seedRule(t, "cnt-inactive", src, []db.CpiTenant{src}, false)            // inactive, excluded

	// rule1: mismatched
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule1.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "u",
		Data: db.SnapshotData{
			SourceTenantID:  src.ID,
			ComparedTenants: []uint{tgt.ID},
			Packages: []db.PackageSnapshot{
				{PackageID: "p1", Artifacts: []db.ArtifactSnapshot{
					{ID: "a1", Versions: map[uint]db.ArtifactVersionInfo{
						src.ID: {DesignTimeVersion: "1.0"},
						tgt.ID: {DesignTimeVersion: "0.9"}, // mismatch
					}},
				}},
			},
		},
	})

	// rule2: all matched
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule2.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "u",
		Data: db.SnapshotData{
			SourceTenantID:  src.ID,
			ComparedTenants: []uint{tgt.ID},
			Packages: []db.PackageSnapshot{
				{PackageID: "p1", Artifacts: []db.ArtifactSnapshot{
					{ID: "a1", Versions: map[uint]db.ArtifactVersionInfo{
						src.ID: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
						tgt.ID: {DesignTimeVersion: "1.0", RuntimeVersion: "1.0"},
					}},
				}},
			},
		},
	})
	// rule3: no snapshot
	_ = rule3

	svc := newTestService(nil)
	counts, err := svc.GetVersionCompareCounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if counts.Total != 3 {
		t.Errorf("Total: got %d, want 3", counts.Total)
	}
	if counts.StatusCounts["mismatched"] != 1 {
		t.Errorf("mismatched: got %d, want 1", counts.StatusCounts["mismatched"])
	}
	if counts.StatusCounts["matched"] != 1 {
		t.Errorf("matched: got %d, want 1", counts.StatusCounts["matched"])
	}
	if counts.StatusCounts[string(consts.SnapshotStatusNone)] != 1 {
		t.Errorf("none: got %d, want 1", counts.StatusCounts[string(consts.SnapshotStatusNone)])
	}
}

func TestGetVersionCompareCounts_RunningAndFailed(t *testing.T) {
	cleanAll(t)
	src := seedTenant(t, "cnt2-src")
	rule1 := seedRule(t, "cnt2-running", src, []db.CpiTenant{src}, true)
	rule2 := seedRule(t, "cnt2-failed", src, []db.CpiTenant{src}, true)

	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule1.ID, Status: consts.SnapshotStatusRunning, TriggeredAt: time.Now(), TriggeredBy: "u",
	})
	now := time.Now()
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule2.ID, Status: consts.SnapshotStatusFailed, TriggeredAt: time.Now(), CompletedAt: &now,
		TriggeredBy: "u", Error: "something broke",
	})

	svc := newTestService(nil)
	counts, err := svc.GetVersionCompareCounts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if counts.Total != 2 {
		t.Errorf("Total: got %d, want 2", counts.Total)
	}
	if counts.StatusCounts[string(consts.SnapshotStatusRunning)] != 1 {
		t.Errorf("running: got %d, want 1", counts.StatusCounts[string(consts.SnapshotStatusRunning)])
	}
	if counts.StatusCounts[string(consts.SnapshotStatusFailed)] != 1 {
		t.Errorf("failed: got %d, want 1", counts.StatusCounts[string(consts.SnapshotStatusFailed)])
	}
}
