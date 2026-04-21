package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/lifecycle"
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
	tc := newTestCleanup(t)

	source := seedTenant(t, tc, "source-tenant")
	target1 := seedTenant(t, tc, "target-tenant-1")
	target2 := seedTenant(t, tc, "target-tenant-2")
	rule := seedRule(t, tc, "query-test-rule", source, []db.CpiTenant{source, target1, target2}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src")
	rule := seedRule(t, tc, "empty-rule", source, []db.CpiTenant{source}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src")
	rule := seedRule(t, tc, "running-rule", source, []db.CpiTenant{source}, true)
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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-trigger")
	rule := seedRule(t, tc, "trigger-rule", source, []db.CpiTenant{source}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-conflict")
	rule := seedRule(t, tc, "conflict-rule", source, []db.CpiTenant{source}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-ratelimit")
	rule := seedRule(t, tc, "ratelimit-rule", source, []db.CpiTenant{source}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-expired")
	rule := seedRule(t, tc, "expired-rule", source, []db.CpiTenant{source}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-collect")
	target := seedTenant(t, tc, "tgt-collect")
	rule := seedRule(t, tc, "collect-rule", source, []db.CpiTenant{source, target}, true)

	// Create initial running snapshot
	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "test",
	})

	// Build mock per tenant
	clients := map[string]*mockCPIClient{
		source.PirApiDestinationName: {
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
		target.PirApiDestinationName: {
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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-err")
	rule := seedRule(t, tc, "err-rule", source, []db.CpiTenant{source}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-pkg-err")
	rule := seedRule(t, tc, "pkgerr-rule", source, []db.CpiTenant{source}, true)

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
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "src-partial")
	target := seedTenant(t, tc, "tgt-partial")
	rule := seedRule(t, tc, "partial-rule", source, []db.CpiTenant{source, target}, true)

	seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "test",
	})

	clients := map[string]*mockCPIClient{
		source.PirApiDestinationName: {
			packages: []cpi.CPIPackage{{ID: "pkg1"}},
			iflows: map[string][]cpi.IflowItem{
				"pkg1": {{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "iflow1", Name: "IFlow", Version: "1.0", PackageID: "pkg1"}}},
			},
			runtimeArts: []cpi.RuntimeArtifact{{ID: "iflow1", Version: "1.0", Status: consts.Artifact_Rt_Started}},
		},
		target.PirApiDestinationName: {
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
	tc := newTestCleanup(t)
	src1 := seedTenant(t, tc, "sum-src1")
	tgt1 := seedTenant(t, tc, "sum-tgt1")
	src2 := seedTenant(t, tc, "sum-src2")

	rule1 := seedRule(t, tc, "sum-rule-1", src1, []db.CpiTenant{src1, tgt1}, true)
	rule2 := seedRule(t, tc, "sum-rule-2", src2, []db.CpiTenant{src2}, true)
	seedRule(t, tc, "inactive-rule", src1, []db.CpiTenant{src1}, false) // should not appear

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

	// Build map of test-created rules from the full result set.
	// Other active rules may exist in the shared DB, so we don't assert exact len.
	itemMap := make(map[uint]VersionCompareSummaryItem)
	for _, it := range items {
		itemMap[it.DeliveryRuleID] = it
	}

	// rule1 must be present
	s1, ok := itemMap[rule1.ID]
	if !ok {
		t.Fatalf("rule1 (ID=%d) not found in summary results", rule1.ID)
	}
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

	// rule2 — no snapshot, must be present
	s2, ok := itemMap[rule2.ID]
	if !ok {
		t.Fatalf("rule2 (ID=%d) not found in summary results", rule2.ID)
	}
	if s2.Status != consts.SnapshotStatusNone {
		t.Errorf("rule2 status: got %q, want %q", s2.Status, consts.SnapshotStatusNone)
	}
}

// =============================================================================
// GetVersionCompareCounts
// =============================================================================

func TestGetVersionCompareCounts(t *testing.T) {
	tc := newTestCleanup(t)
	src := seedTenant(t, tc, "cnt-src")
	tgt := seedTenant(t, tc, "cnt-tgt")

	rule1 := seedRule(t, tc, "cnt-rule-1", src, []db.CpiTenant{src, tgt}, true) // will have mismatch
	rule2 := seedRule(t, tc, "cnt-rule-2", src, []db.CpiTenant{src, tgt}, true) // will be matched
	rule3 := seedRule(t, tc, "cnt-rule-3", src, []db.CpiTenant{src}, true)      // no snapshot
	seedRule(t, tc, "cnt-inactive", src, []db.CpiTenant{src}, false)            // inactive, excluded

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

	// Other active rules may exist in the shared DB, so assert minimum counts.
	if counts.Total < 3 {
		t.Errorf("Total: got %d, want >= 3", counts.Total)
	}
	if counts.StatusCounts["mismatched"] < 1 {
		t.Errorf("mismatched: got %d, want >= 1", counts.StatusCounts["mismatched"])
	}
	if counts.StatusCounts["matched"] < 1 {
		t.Errorf("matched: got %d, want >= 1", counts.StatusCounts["matched"])
	}
	if counts.StatusCounts[string(consts.SnapshotStatusNone)] < 1 {
		t.Errorf("none: got %d, want >= 1", counts.StatusCounts[string(consts.SnapshotStatusNone)])
	}
}

func TestGetVersionCompareCounts_RunningAndFailed(t *testing.T) {
	tc := newTestCleanup(t)
	src := seedTenant(t, tc, "cnt2-src")
	rule1 := seedRule(t, tc, "cnt2-running", src, []db.CpiTenant{src}, true)
	rule2 := seedRule(t, tc, "cnt2-failed", src, []db.CpiTenant{src}, true)

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
	// Other active rules may exist in the shared DB, so assert minimum counts.
	if counts.Total < 2 {
		t.Errorf("Total: got %d, want >= 2", counts.Total)
	}
	if counts.StatusCounts[string(consts.SnapshotStatusRunning)] < 1 {
		t.Errorf("running: got %d, want >= 1", counts.StatusCounts[string(consts.SnapshotStatusRunning)])
	}
	if counts.StatusCounts[string(consts.SnapshotStatusFailed)] < 1 {
		t.Errorf("failed: got %d, want >= 1", counts.StatusCounts[string(consts.SnapshotStatusFailed)])
	}
}

// --- Included Packages (Global Whitelist) Tests ---

// cleanIncludedPackages removes all VersionCompareIncludedPackage records.
func cleanIncludedPackages(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testDB.Unscoped().Where("1 = 1").Delete(&db.VersionCompareIncludedPackage{})
	})
}

func TestGetIncludedPackages_Empty(t *testing.T) {
	cleanIncludedPackages(t)
	svc := newTestService(nil)

	packages, err := svc.GetIncludedPackages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("expected empty list, got %d items", len(packages))
	}
}

func TestUpdateIncludedPackages_InsertAndReplace(t *testing.T) {
	cleanIncludedPackages(t)
	svc := newTestService(nil)

	// Initial insert
	inputs := []IncludedPackageInput{
		{PackageID: "PkgA", Description: "Package A"},
		{PackageID: "PkgB", Description: "Package B"},
	}
	result, err := svc.UpdateIncludedPackages(inputs, "user@test.com")
	if err != nil {
		t.Fatalf("UpdateIncludedPackages failed: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].PackageID != "PkgA" || result[1].PackageID != "PkgB" {
		t.Errorf("unexpected package IDs: %v, %v", result[0].PackageID, result[1].PackageID)
	}
	if result[0].CreatedBy != "user@test.com" {
		t.Errorf("CreatedBy: got %q, want user@test.com", result[0].CreatedBy)
	}

	// Verify via Get
	packages, err := svc.GetIncludedPackages()
	if err != nil {
		t.Fatalf("GetIncludedPackages failed: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(packages))
	}

	// Replace with different set
	inputs2 := []IncludedPackageInput{
		{PackageID: "PkgX", Description: "Package X"},
	}
	result2, err := svc.UpdateIncludedPackages(inputs2, "admin@test.com")
	if err != nil {
		t.Fatalf("UpdateIncludedPackages (replace) failed: %v", err)
	}
	if len(result2) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result2))
	}
	if result2[0].PackageID != "PkgX" {
		t.Errorf("PackageID: got %q, want PkgX", result2[0].PackageID)
	}

	// Verify old ones are gone
	packages2, err := svc.GetIncludedPackages()
	if err != nil {
		t.Fatalf("GetIncludedPackages (after replace) failed: %v", err)
	}
	if len(packages2) != 1 {
		t.Fatalf("expected 1 package after replace, got %d", len(packages2))
	}
}

func TestUpdateIncludedPackages_EmptyList(t *testing.T) {
	cleanIncludedPackages(t)
	svc := newTestService(nil)

	// Insert some packages first
	_, err := svc.UpdateIncludedPackages([]IncludedPackageInput{
		{PackageID: "PkgA"},
	}, "user@test.com")
	if err != nil {
		t.Fatalf("initial insert failed: %v", err)
	}

	// Replace with empty list
	result, err := svc.UpdateIncludedPackages([]IncludedPackageInput{}, "user@test.com")
	if err != nil {
		t.Fatalf("UpdateIncludedPackages (empty) failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 results, got %d", len(result))
	}

	// Verify table is empty
	packages, err := svc.GetIncludedPackages()
	if err != nil {
		t.Fatalf("GetIncludedPackages failed: %v", err)
	}
	if len(packages) != 0 {
		t.Errorf("expected empty list, got %d items", len(packages))
	}
}

func TestTrigger_WithIncludedPackagesFilter(t *testing.T) {
	cleanIncludedPackages(t)
	tc := newTestCleanup(t)
	tc.cleanIncludedPkgs = true

	source := seedTenant(t, tc, "vc-incl-src")
	target := seedTenant(t, tc, "vc-incl-tgt")
	rule := seedRule(t, tc, "vc-incl-rule", source, []db.CpiTenant{source, target}, true)

	mock := &mockCPIClient{
		packages: []cpi.CPIPackage{
			{ID: "PkgA"},
			{ID: "PkgB"},
			{ID: "TemplatePkg"}, // should be excluded when whitelist is active
		},
		iflows: map[string][]cpi.IflowItem{
			"PkgA":        {{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "FlowA", Name: "Flow A", Version: "1.0.0"}}},
			"PkgB":        {{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "FlowB", Name: "Flow B", Version: "2.0.0"}}},
			"TemplatePkg": {{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "FlowT", Name: "Flow T", Version: "1.0.0"}}},
		},
		runtimeArts: []cpi.RuntimeArtifact{
			{ID: "FlowA", Version: "1.0.0"},
			{ID: "FlowB", Version: "2.0.0"},
			{ID: "FlowT", Version: "1.0.0"},
		},
	}
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return mock, nil
	}
	svc := newTestService(factory)

	// Insert whitelist: only PkgA and PkgB
	_, err := svc.UpdateIncludedPackages([]IncludedPackageInput{
		{PackageID: "PkgA"},
		{PackageID: "PkgB"},
	}, "test@test.com")
	if err != nil {
		t.Fatalf("UpdateIncludedPackages failed: %v", err)
	}

	// Trigger
	result, err := svc.TriggerVersionCompare(rule.ID, "test@test.com")
	if err != nil {
		t.Fatalf("TriggerVersionCompare failed: %v", err)
	}
	if result.Status != consts.TriggerStatusRunning {
		t.Fatalf("expected running, got %s", result.Status)
	}

	waitForSnapshotComplete(t, rule.ID, 5*time.Second)

	// Query and verify TemplatePkg is excluded
	resp, err := svc.QueryVersionCompare(rule.ID, VersionCompareQueryParams{
		DesignTime: true,
		RunTime:    true,
	})
	if err != nil {
		t.Fatalf("QueryVersionCompare failed: %v", err)
	}
	if resp.Status != consts.SnapshotStatusCompleted {
		t.Fatalf("expected completed, got %s", resp.Status)
	}

	// Should only have PkgA and PkgB, not TemplatePkg
	if len(resp.Packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(resp.Packages))
	}
	pkgIDs := map[string]bool{}
	for _, pkg := range resp.Packages {
		pkgIDs[pkg.PackageID] = true
	}
	if !pkgIDs["PkgA"] {
		t.Error("expected PkgA in results")
	}
	if !pkgIDs["PkgB"] {
		t.Error("expected PkgB in results")
	}
	if pkgIDs["TemplatePkg"] {
		t.Error("TemplatePkg should be excluded by whitelist")
	}
}

func TestTrigger_EmptyWhitelistIncludesAll(t *testing.T) {
	cleanIncludedPackages(t)
	tc := newTestCleanup(t)

	source := seedTenant(t, tc, "vc-empty-incl-src")
	target := seedTenant(t, tc, "vc-empty-incl-tgt")
	rule := seedRule(t, tc, "vc-empty-incl-rule", source, []db.CpiTenant{source, target}, true)

	mock := &mockCPIClient{
		packages: []cpi.CPIPackage{
			{ID: "PkgA"},
			{ID: "PkgB"},
		},
		iflows: map[string][]cpi.IflowItem{
			"PkgA": {{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "FlowA", Name: "Flow A", Version: "1.0.0"}}},
			"PkgB": {{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: "FlowB", Name: "Flow B", Version: "2.0.0"}}},
		},
		runtimeArts: []cpi.RuntimeArtifact{},
	}
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return mock, nil
	}
	svc := newTestService(factory)

	// Whitelist is empty → should include all packages
	result, err := svc.TriggerVersionCompare(rule.ID, "test@test.com")
	if err != nil {
		t.Fatalf("TriggerVersionCompare failed: %v", err)
	}
	if result.Status != consts.TriggerStatusRunning {
		t.Fatalf("expected running, got %s", result.Status)
	}

	waitForSnapshotComplete(t, rule.ID, 5*time.Second)

	resp, err := svc.QueryVersionCompare(rule.ID, VersionCompareQueryParams{
		DesignTime: true,
	})
	if err != nil {
		t.Fatalf("QueryVersionCompare failed: %v", err)
	}

	// Both packages should be present
	if len(resp.Packages) != 2 {
		t.Errorf("expected 2 packages with empty whitelist, got %d", len(resp.Packages))
	}
}

// =============================================================================
// Phase 2 Tests: PreviewDRFromMismatch
// =============================================================================

// setupPreviewTestData creates a snapshot with mismatches of various types for preview tests.
func setupPreviewTestData(t *testing.T) (ruleID uint, snapshotCompletedAt time.Time, tc *testCleanup) {
	t.Helper()
	tc = newTestCleanup(t)

	source := seedTenant(t, tc, "prev-src-"+t.Name())
	target := seedTenant(t, tc, "prev-tgt-"+t.Name())
	rule := seedRule(t, tc, "prev-rule-"+t.Name(), source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "1.0.*"
	testDB.Save(&rule)

	now := time.Now().Truncate(time.Second)
	snap := db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    now,
		CompletedAt:    &now,
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							// Includable: version matches pattern, not draft, not duplicate
							ID: "iflow-ok", Name: "IFlow OK", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.5"},
								target.ID: {DesignTimeVersion: "1.0.4"}, // mismatch
							},
						},
						{
							// Draft: source has "active" DT version
							ID: "iflow-draft", Name: "IFlow Draft", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "active"},
								target.ID: {DesignTimeVersion: "1.0.0"},
							},
						},
						{
							// VersionPattern mismatch: version 2.1.0 doesn't match 1.0.*
							ID: "iflow-badver", Name: "IFlow BadVer", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "2.1.0"},
								target.ID: {DesignTimeVersion: "1.0.0"},
							},
						},
						{
							// Fully matched — should NOT appear in preview
							ID: "iflow-matched", Name: "IFlow Matched", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.5"},
								target.ID: {DesignTimeVersion: "1.0.5"}, // no mismatch
							},
						},
					},
				},
				{
					PackageID: "pkg2",
					Artifacts: []db.ArtifactSnapshot{
						{
							// Target absent from snapshot → mismatch (Preview treats missing as mismatch)
							ID: "iflow-missing-tgt", Name: "IFlow Missing Target", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.1"},
								// target not present in map
							},
						},
					},
				},
			},
		},
	}
	seedSnapshot(t, snap)

	return rule.ID, now, tc
}

func TestPreviewDR_Basic(t *testing.T) {
	ruleID, _, _ := setupPreviewTestData(t)
	svc := newTestService(nil)

	resp, err := svc.PreviewDRFromMismatch(ruleID)
	if err != nil {
		t.Fatalf("PreviewDRFromMismatch failed: %v", err)
	}

	// 4 mismatched artifacts (iflow-ok, iflow-draft, iflow-badver, iflow-missing-tgt)
	// iflow-matched is NOT a mismatch
	if resp.Summary.TotalMismatch != 4 {
		t.Errorf("TotalMismatch: got %d, want 4", resp.Summary.TotalMismatch)
	}
	if len(resp.Artifacts) != 4 {
		t.Errorf("Artifacts count: got %d, want 4", len(resp.Artifacts))
	}

	// Check that snapshot info is returned
	if resp.SnapshotID == 0 {
		t.Error("SnapshotID should be non-zero")
	}
	if resp.SnapshotCompletedAt.IsZero() {
		t.Error("SnapshotCompletedAt should be set")
	}
}

func TestPreviewDR_DraftDetection(t *testing.T) {
	ruleID, _, _ := setupPreviewTestData(t)
	svc := newTestService(nil)

	resp, err := svc.PreviewDRFromMismatch(ruleID)
	if err != nil {
		t.Fatalf("PreviewDRFromMismatch failed: %v", err)
	}

	// Find iflow-draft
	var draftArt *PreviewDRArtifact
	for i := range resp.Artifacts {
		if resp.Artifacts[i].ArtifactID == "iflow-draft" {
			draftArt = &resp.Artifacts[i]
			break
		}
	}
	if draftArt == nil {
		t.Fatal("iflow-draft not found in artifacts")
	}
	if draftArt.Category != "draft" {
		t.Errorf("iflow-draft category: got %q, want %q", draftArt.Category, "draft")
	}
	if resp.Summary.Draft != 1 {
		t.Errorf("Draft count: got %d, want 1", resp.Summary.Draft)
	}
}

func TestPreviewDR_VersionPatternDetection(t *testing.T) {
	ruleID, _, _ := setupPreviewTestData(t)
	svc := newTestService(nil)

	resp, err := svc.PreviewDRFromMismatch(ruleID)
	if err != nil {
		t.Fatalf("PreviewDRFromMismatch failed: %v", err)
	}

	var vpArt *PreviewDRArtifact
	for i := range resp.Artifacts {
		if resp.Artifacts[i].ArtifactID == "iflow-badver" {
			vpArt = &resp.Artifacts[i]
			break
		}
	}
	if vpArt == nil {
		t.Fatal("iflow-badver not found in artifacts")
	}
	if vpArt.Category != "versionPattern" {
		t.Errorf("iflow-badver category: got %q, want %q", vpArt.Category, "versionPattern")
	}
	if resp.Summary.VersionPattern != 1 {
		t.Errorf("VersionPattern count: got %d, want 1", resp.Summary.VersionPattern)
	}
}

func TestPreviewDR_DuplicateDetection(t *testing.T) {
	tc := newTestCleanup(t)

	source := seedTenant(t, tc, "dup-src-"+t.Name())
	target := seedTenant(t, tc, "dup-tgt-"+t.Name())
	rule := seedRule(t, tc, "dup-rule-"+t.Name(), source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "1.0.*"
	testDB.Save(&rule)

	now := time.Now().Truncate(time.Second)
	snap := db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    now,
		CompletedAt:    &now,
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							ID: "iflow-dup", Name: "IFlow Dup", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.5"},
								target.ID: {DesignTimeVersion: "1.0.4"},
							},
						},
					},
				},
			},
		},
	}
	seedSnapshot(t, snap)

	// Create an active DR that already contains iflow-dup at version 1.0.5
	art := seedArtifact(t, tc, db.Artifact{
		TechID:    "iflow-dup",
		Version:   "1.0.5",
		Name:      "IFlow Dup",
		Type:      consts.Artifact_Type_Iflow,
		PackageID: "pkg1",
	})
	existingDR := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "Existing DR",
		DeliveryRuleID:  rule.ID,
		SourceTenantID:  source.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "user",
		UpdatedBy:       "user",
	})
	seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID:      existingDR.ID,
		TenantID:               target.ID,
		ArtifactID:             art.ID,
		ArtifactTechID:         art.TechID,
		ArtifactVersion:        art.Version,
		TransportRequestNumber: "",
		RequestState:           lifecycle.RequestPending,
		ImportState:            lifecycle.ImportNotStarted,
		DeployState:            lifecycle.DeployNotStarted,
	})

	svc := newTestService(nil)
	resp, err := svc.PreviewDRFromMismatch(rule.ID)
	if err != nil {
		t.Fatalf("PreviewDRFromMismatch failed: %v", err)
	}

	if resp.Summary.Duplicate != 1 {
		t.Errorf("Duplicate count: got %d, want 1", resp.Summary.Duplicate)
	}
	var dupArt *PreviewDRArtifact
	for i := range resp.Artifacts {
		if resp.Artifacts[i].ArtifactID == "iflow-dup" {
			dupArt = &resp.Artifacts[i]
			break
		}
	}
	if dupArt == nil {
		t.Fatal("iflow-dup not found")
	}
	if dupArt.Category != "duplicate" {
		t.Errorf("category: got %q, want %q", dupArt.Category, "duplicate")
	}
	if dupArt.ExistingDR == nil {
		t.Fatal("ExistingDR should not be nil for duplicate")
	}
	if dupArt.ExistingDR.ID != existingDR.ID {
		t.Errorf("ExistingDR.ID: got %d, want %d", dupArt.ExistingDR.ID, existingDR.ID)
	}
}

func TestPreviewDR_NoMismatch(t *testing.T) {
	tc := newTestCleanup(t)

	source := seedTenant(t, tc, "nomis-src-"+t.Name())
	target := seedTenant(t, tc, "nomis-tgt-"+t.Name())
	rule := seedRule(t, tc, "nomis-rule-"+t.Name(), source, []db.CpiTenant{source, target}, true)

	now := time.Now().Truncate(time.Second)
	snap := db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    now,
		CompletedAt:    &now,
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							ID: "iflow-all-match", Name: "All Match", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.0"},
								target.ID: {DesignTimeVersion: "1.0.0"},
							},
						},
					},
				},
			},
		},
	}
	seedSnapshot(t, snap)

	svc := newTestService(nil)
	_, err := svc.PreviewDRFromMismatch(rule.ID)
	if err == nil {
		t.Fatal("PreviewDRFromMismatch with no mismatches should return error")
	}
}

func TestPreviewDR_NoSnapshot(t *testing.T) {
	tc := newTestCleanup(t)
	source := seedTenant(t, tc, "nosnap-src-"+t.Name())
	rule := seedRule(t, tc, "nosnap-rule-"+t.Name(), source, []db.CpiTenant{source}, true)

	svc := newTestService(nil)
	_, err := svc.PreviewDRFromMismatch(rule.ID)
	if err == nil {
		t.Fatal("PreviewDRFromMismatch with no snapshot should return error")
	}
}

// =============================================================================
// Phase 2 Tests: CreateDRFromMismatch
// =============================================================================

func setupCreateTestData(t *testing.T) (ruleID uint, snapshotID uint, snapshotCompletedAt time.Time, sourceID uint, tc *testCleanup) {
	t.Helper()
	tc = newTestCleanup(t)

	source := seedTenant(t, tc, "cre-src-"+t.Name())
	source.TransportNodeID = 500
	source.TransportNodeName = "cre-src-node"
	testDB.Save(&source)

	target := seedTenant(t, tc, "cre-tgt-"+t.Name())
	target.TransportNodeID = 600
	target.TransportNodeName = "cre-tgt-node"
	testDB.Save(&target)

	rule := seedRule(t, tc, "cre-rule-"+t.Name(), source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "1.0.*"
	testDB.Save(&rule)

	now := time.Now().Truncate(time.Second)
	snap := seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    now,
		CompletedAt:    &now,
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							ID: "cre-iflow-1", Name: "Create IFlow 1", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.5"},
								target.ID: {DesignTimeVersion: "1.0.4"},
							},
						},
						{
							ID: "cre-iflow-2", Name: "Create IFlow 2", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.3"},
								target.ID: {DesignTimeVersion: "1.0.2"},
							},
						},
					},
				},
			},
		},
	})

	return rule.ID, snap.ID, now, source.ID, tc
}

func TestCreateDR_Basic(t *testing.T) {
	ruleID, snapshotID, completedAt, _, tc := setupCreateTestData(t)

	// CPI mock for downgrade check — target has lower version, so no downgrade error
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	req := CreateDRFromMismatchRequest{
		SnapshotID:          snapshotID,
		SnapshotCompletedAt: completedAt,
		ArtifactKeys: []ArtifactKey{
			{ArtifactID: "cre-iflow-1", PackageID: "pkg1"},
			{ArtifactID: "cre-iflow-2", PackageID: "pkg1"},
		},
	}

	resp, err := svc.CreateDRFromMismatch(ruleID, req, "test-user")
	if err != nil {
		t.Fatalf("CreateDRFromMismatch failed: %v", err)
	}
	tc.trackDR(resp.DeliveryRequest.ID)

	if resp.Summary.Requested != 2 {
		t.Errorf("Requested: got %d, want 2", resp.Summary.Requested)
	}
	if resp.Summary.Created != 2 {
		t.Errorf("Created: got %d, want 2", resp.Summary.Created)
	}
	if resp.DeliveryRequest.ID == 0 {
		t.Fatal("DR ID should be non-zero")
	}
	if resp.DeliveryRequest.AggregateStatus != lifecycle.AggPending {
		t.Errorf("DR status: got %s, want PENDING", resp.DeliveryRequest.AggregateStatus)
	}
	if resp.DeliveryRequest.VersionCompareSnapshotID == nil {
		t.Fatal("VersionCompareSnapshotID should be set")
	}
	if *resp.DeliveryRequest.VersionCompareSnapshotID != snapshotID {
		t.Errorf("VersionCompareSnapshotID: got %d, want %d", *resp.DeliveryRequest.VersionCompareSnapshotID, snapshotID)
	}

	// Verify ops have empty TR
	for _, op := range resp.DeliveryRequest.ArtifactTenantOperations {
		if op.TransportRequestNumber != "" {
			t.Errorf("op %d should have empty TR, got %q", op.ID, op.TransportRequestNumber)
		}
	}
}

func TestCreateDR_SnapshotStale(t *testing.T) {
	ruleID, snapshotID, _, _, _ := setupCreateTestData(t)

	svc := newTestService(nil)

	// Use a stale completedAt
	staleTime := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	req := CreateDRFromMismatchRequest{
		SnapshotID:          snapshotID,
		SnapshotCompletedAt: staleTime,
		ArtifactKeys: []ArtifactKey{
			{ArtifactID: "cre-iflow-1", PackageID: "pkg1"},
		},
	}

	_, err := svc.CreateDRFromMismatch(ruleID, req, "test-user")
	if err == nil {
		t.Fatal("CreateDRFromMismatch with stale snapshot should fail")
	}
}

func TestCreateDR_EmptyArtifactKeys(t *testing.T) {
	ruleID, snapshotID, completedAt, _, _ := setupCreateTestData(t)

	svc := newTestService(nil)

	req := CreateDRFromMismatchRequest{
		SnapshotID:          snapshotID,
		SnapshotCompletedAt: completedAt,
		ArtifactKeys:        []ArtifactKey{}, // empty
	}

	_, err := svc.CreateDRFromMismatch(ruleID, req, "test-user")
	if err == nil {
		t.Fatal("CreateDRFromMismatch with empty artifactKeys should fail")
	}
}

func TestCreateDR_JiraRequired(t *testing.T) {
	tc := newTestCleanup(t)

	source := seedTenant(t, tc, "jira-src-"+t.Name())
	target := seedTenant(t, tc, "jira-tgt-"+t.Name())
	rule := seedRule(t, tc, "jira-rule-"+t.Name(), source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "1.0.*"
	rule.RequireJira = true
	testDB.Save(&rule)

	now := time.Now().Truncate(time.Second)
	snap := seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    now,
		CompletedAt:    &now,
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							ID: "jira-iflow", Name: "Jira IFlow", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.1"},
								target.ID: {DesignTimeVersion: "1.0.0"},
							},
						},
					},
				},
			},
		},
	})

	svc := newTestService(nil)

	// No JIRA link → should fail
	req := CreateDRFromMismatchRequest{
		SnapshotID:          snap.ID,
		SnapshotCompletedAt: now,
		JiraLink:            "",
		ArtifactKeys:        []ArtifactKey{{ArtifactID: "jira-iflow", PackageID: "pkg1"}},
	}
	_, err := svc.CreateDRFromMismatch(rule.ID, req, "test-user")
	if err == nil {
		t.Fatal("CreateDRFromMismatch without JIRA when required should fail")
	}

	// With JIRA link → should succeed (assuming CPI mock for downgrade passes)
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc2 := newTestService(factory)

	req.JiraLink = "https://jira.example.com/PROJ-123"
	resp, err := svc2.CreateDRFromMismatch(rule.ID, req, "test-user")
	if err != nil {
		t.Fatalf("CreateDRFromMismatch with JIRA should succeed: %v", err)
	}
	tc.trackDR(resp.DeliveryRequest.ID)
	if resp.DeliveryRequest.JiraLink != "https://jira.example.com/PROJ-123" {
		t.Errorf("JiraLink: got %q", resp.DeliveryRequest.JiraLink)
	}
}

func TestCreateDR_SnapshotFKSet(t *testing.T) {
	ruleID, snapshotID, completedAt, _, tc := setupCreateTestData(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	req := CreateDRFromMismatchRequest{
		SnapshotID:          snapshotID,
		SnapshotCompletedAt: completedAt,
		ArtifactKeys: []ArtifactKey{
			{ArtifactID: "cre-iflow-1", PackageID: "pkg1"},
		},
	}

	resp, err := svc.CreateDRFromMismatch(ruleID, req, "test-user")
	if err != nil {
		t.Fatalf("CreateDRFromMismatch failed: %v", err)
	}
	tc.trackDR(resp.DeliveryRequest.ID)

	// Verify FK in DB
	var dbDR db.DeliveryRequest
	testDB.First(&dbDR, resp.DeliveryRequest.ID)
	if dbDR.VersionCompareSnapshotID == nil {
		t.Fatal("VersionCompareSnapshotID should be set in DB")
	}
	if *dbDR.VersionCompareSnapshotID != snapshotID {
		t.Errorf("VersionCompareSnapshotID in DB: got %d, want %d", *dbDR.VersionCompareSnapshotID, snapshotID)
	}
}

func TestCreateDR_DuplicateIncluded(t *testing.T) {
	// Test that user can intentionally select an artifact that was marked as duplicate in preview.
	// The Create API should include it normally.
	tc := newTestCleanup(t)

	source := seedTenant(t, tc, "dupinc-src-"+t.Name())
	target := seedTenant(t, tc, "dupinc-tgt-"+t.Name())
	rule := seedRule(t, tc, "dupinc-rule-"+t.Name(), source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "1.0.*"
	testDB.Save(&rule)

	now := time.Now().Truncate(time.Second)
	snap := seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    now,
		CompletedAt:    &now,
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							ID: "dup-allowed-iflow", Name: "Dup Allowed", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.7"},
								target.ID: {DesignTimeVersion: "1.0.6"},
							},
						},
					},
				},
			},
		},
	})

	// Create an existing active DR with the same artifact
	dupArt := seedArtifact(t, tc, db.Artifact{
		TechID:    "dup-allowed-iflow",
		Version:   "1.0.7",
		Name:      "Dup Allowed",
		Type:      consts.Artifact_Type_Iflow,
		PackageID: "pkg1",
	})
	existingDR := seedDeliveryRequest(t, tc, db.DeliveryRequest{
		Name:            "Existing Dup DR",
		DeliveryRuleID:  rule.ID,
		SourceTenantID:  source.ID,
		AggregateStatus: lifecycle.AggPending,
		CreatedBy:       "user",
		UpdatedBy:       "user",
	})
	seedOp(t, db.ArtifactTenantOperation{
		DeliveryRequestID: existingDR.ID,
		TenantID:          target.ID,
		ArtifactID:        dupArt.ID,
		ArtifactTechID:    dupArt.TechID,
		ArtifactVersion:   dupArt.Version,
		RequestState:      lifecycle.RequestPending,
		ImportState:       lifecycle.ImportNotStarted,
		DeployState:       lifecycle.DeployNotStarted,
	})

	// Preview should show it as duplicate
	svc := newTestService(nil)
	preview, err := svc.PreviewDRFromMismatch(rule.ID)
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if preview.Summary.Duplicate != 1 {
		t.Errorf("Preview should show 1 duplicate, got %d", preview.Summary.Duplicate)
	}

	// Create should still work when user selects the duplicate
	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc2 := newTestService(factory)

	req := CreateDRFromMismatchRequest{
		SnapshotID:          snap.ID,
		SnapshotCompletedAt: now,
		ArtifactKeys:        []ArtifactKey{{ArtifactID: "dup-allowed-iflow", PackageID: "pkg1"}},
	}
	resp, err := svc2.CreateDRFromMismatch(rule.ID, req, "test-user")
	if err != nil {
		t.Fatalf("CreateDRFromMismatch with duplicate selection should succeed: %v", err)
	}
	tc.trackDR(resp.DeliveryRequest.ID)

	if resp.Summary.Created != 1 {
		t.Errorf("Created: got %d, want 1", resp.Summary.Created)
	}
}

func TestCreateDR_VersionDowngradeSkip(t *testing.T) {
	tc := newTestCleanup(t)

	source := seedTenant(t, tc, "dg-src-"+t.Name())
	source.TransportNodeID = 700
	source.TransportNodeName = "dg-src-node"
	testDB.Save(&source)

	target := seedTenant(t, tc, "dg-tgt-"+t.Name())
	target.TransportNodeID = 800
	target.TransportNodeName = "dg-tgt-node"
	testDB.Save(&target)

	rule := seedRule(t, tc, "dg-rule-"+t.Name(), source, []db.CpiTenant{source, target}, true)
	rule.VersionPattern = "1.0.*"
	testDB.Save(&rule)

	now := time.Now().Truncate(time.Second)
	snap := seedSnapshot(t, db.VersionCompareSnapshot{
		DeliveryRuleID: rule.ID,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    now,
		CompletedAt:    &now,
		TriggeredBy:    "test",
		Data: db.SnapshotData{
			SourceTenantID:  source.ID,
			ComparedTenants: []uint{target.ID},
			Packages: []db.PackageSnapshot{
				{
					PackageID: "pkg1",
					Artifacts: []db.ArtifactSnapshot{
						{
							// This artifact has source version LOWER than target → downgrade
							ID: "dg-iflow", Name: "Downgrade IFlow", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.1"},
								target.ID: {DesignTimeVersion: "1.0.5"}, // target is higher
							},
						},
						{
							// This artifact is fine (source higher than target)
							ID: "ok-iflow", Name: "OK IFlow", Type: "Integration Flow",
							Versions: map[uint]db.ArtifactVersionInfo{
								source.ID: {DesignTimeVersion: "1.0.9"},
								target.ID: {DesignTimeVersion: "1.0.8"},
							},
						},
					},
				},
			},
		},
	})

	// CPI mock: GetDesignTimeIflow returns the target's current version
	// For the downgrade check, checkVersionDowngradeInTenant calls GetDesignTimeIflow(ctx, techID, "active")
	// and compares the returned version to the source version.
	factoryWithDowngrade := func(ctx context.Context, tenant string) (IntegrationService, error) {
		// If this is the target tenant, GetDesignTimeIflow returns version 1.0.5 (for dg-iflow)
		// and 1.0.8 (for ok-iflow)
		if tenant == target.PirApiDestinationName {
			return &mockCPIClientWithDesignTime{
				iflowVersions: map[string]string{
					"dg-iflow": "1.0.5", // higher than source 1.0.1 → downgrade
					"ok-iflow": "1.0.8", // lower than source 1.0.9 → ok
				},
			}, nil
		}
		return &mockCPIClient{}, nil
	}

	svc := newTestService(factoryWithDowngrade)

	req := CreateDRFromMismatchRequest{
		SnapshotID:          snap.ID,
		SnapshotCompletedAt: now,
		ArtifactKeys: []ArtifactKey{
			{ArtifactID: "dg-iflow", PackageID: "pkg1"},
			{ArtifactID: "ok-iflow", PackageID: "pkg1"},
		},
	}

	resp, err := svc.CreateDRFromMismatch(rule.ID, req, "test-user")
	if err != nil {
		t.Fatalf("CreateDRFromMismatch should succeed with partial errors: %v", err)
	}
	tc.trackDR(resp.DeliveryRequest.ID)

	// dg-iflow should be skipped (downgrade), ok-iflow should succeed
	if resp.Summary.Created != 1 {
		t.Errorf("Created: got %d, want 1", resp.Summary.Created)
	}
	if len(resp.Summary.Errors) != 1 {
		t.Errorf("Errors: got %d, want 1", len(resp.Summary.Errors))
	}
	if len(resp.Summary.Errors) > 0 && resp.Summary.Errors[0].ArtifactID != "dg-iflow" {
		t.Errorf("Error artifact: got %q, want %q", resp.Summary.Errors[0].ArtifactID, "dg-iflow")
	}
}

func TestCreateDR_AutoGeneratedName(t *testing.T) {
	ruleID, snapshotID, completedAt, _, tc := setupCreateTestData(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	req := CreateDRFromMismatchRequest{
		Name:                "", // empty → auto-generate
		SnapshotID:          snapshotID,
		SnapshotCompletedAt: completedAt,
		ArtifactKeys: []ArtifactKey{
			{ArtifactID: "cre-iflow-1", PackageID: "pkg1"},
		},
	}

	resp, err := svc.CreateDRFromMismatch(ruleID, req, "test-user")
	if err != nil {
		t.Fatalf("CreateDRFromMismatch failed: %v", err)
	}
	tc.trackDR(resp.DeliveryRequest.ID)

	// Name should follow the format: "Auto DR - <rule name> - VC <snapshot completion time>"
	if resp.DeliveryRequest.Name == "" {
		t.Fatal("DR name should be auto-generated, got empty")
	}
	// Just check it starts with "Auto DR"
	if len(resp.DeliveryRequest.Name) < 7 || resp.DeliveryRequest.Name[:7] != "Auto DR" {
		t.Errorf("DR name should start with 'Auto DR', got %q", resp.DeliveryRequest.Name)
	}
}

func TestCreateDR_SkipDeploy(t *testing.T) {
	ruleID, snapshotID, completedAt, _, tc := setupCreateTestData(t)

	factory := func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClient{}, nil
	}
	svc := newTestService(factory)

	req := CreateDRFromMismatchRequest{
		SnapshotID:          snapshotID,
		SnapshotCompletedAt: completedAt,
		ArtifactKeys: []ArtifactKey{
			{ArtifactID: "cre-iflow-1", PackageID: "pkg1", SkipDeploy: true},
			{ArtifactID: "cre-iflow-2", PackageID: "pkg1", SkipDeploy: false},
		},
	}

	resp, err := svc.CreateDRFromMismatch(ruleID, req, "test-user")
	if err != nil {
		t.Fatalf("CreateDRFromMismatch with SkipDeploy failed: %v", err)
	}
	tc.trackDR(resp.DeliveryRequest.ID)

	if resp.Summary.Created != 2 {
		t.Fatalf("Created: got %d, want 2", resp.Summary.Created)
	}

	// Verify SkipDeploy and DeployState for each op
	for _, op := range resp.DeliveryRequest.ArtifactTenantOperations {
		switch op.ArtifactTechID {
		case "cre-iflow-1":
			if !op.SkipDeploy {
				t.Errorf("cre-iflow-1: expected SkipDeploy=true")
			}
			if op.DeployState != lifecycle.DeployDisabled {
				t.Errorf("cre-iflow-1: expected DEPLOY_DISABLED, got %s", op.DeployState)
			}
		case "cre-iflow-2":
			if op.SkipDeploy {
				t.Errorf("cre-iflow-2: expected SkipDeploy=false")
			}
			if op.DeployState != lifecycle.DeployNotStarted {
				t.Errorf("cre-iflow-2: expected NOT_STARTED, got %s", op.DeployState)
			}
		}
	}
}
