package db

import (
	"fmt"
	"mmt-delivery/consts"
	"testing"
	"time"
)

// --- Create & Read ---

func TestVersionCompareSnapshot_CreateAndRead(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 100)

	now := time.Now().Truncate(time.Microsecond) // pg precision: microsecond
	snap := VersionCompareSnapshot{
		DeliveryRuleID: 100,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    now,
		TriggeredBy:    "user@example.com",
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if snap.ID == 0 {
		t.Fatal("expected non-zero ID after Create")
	}

	var loaded VersionCompareSnapshot
	if err := testDB.First(&loaded, snap.ID).Error; err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if loaded.DeliveryRuleID != 100 {
		t.Errorf("DeliveryRuleID: got %d, want 100", loaded.DeliveryRuleID)
	}
	if loaded.Status != consts.SnapshotStatusRunning {
		t.Errorf("Status: got %q, want %q", loaded.Status, consts.SnapshotStatusRunning)
	}
	if loaded.TriggeredBy != "user@example.com" {
		t.Errorf("TriggeredBy: got %q, want %q", loaded.TriggeredBy, "user@example.com")
	}
	if !loaded.TriggeredAt.Equal(now) {
		t.Errorf("TriggeredAt: got %v, want %v", loaded.TriggeredAt, now)
	}
	if loaded.CompletedAt != nil {
		t.Errorf("CompletedAt: expected nil, got %v", loaded.CompletedAt)
	}
}

// --- UniqueIndex on DeliveryRuleID ---

func TestVersionCompareSnapshot_UniqueIndex(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 200)

	snap1 := VersionCompareSnapshot{
		DeliveryRuleID: 200,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user1",
	}
	if err := testDB.Create(&snap1).Error; err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	snap2 := VersionCompareSnapshot{
		DeliveryRuleID: 200,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user2",
	}
	err := testDB.Create(&snap2).Error
	if err == nil {
		t.Fatal("expected unique constraint violation, got nil")
	}
}

// --- SnapshotData JSON Serialization Round-Trip ---

func TestVersionCompareSnapshot_JSONRoundTrip(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 300)

	completedAt := time.Now().Truncate(time.Microsecond)
	data := SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2, 3},
		Packages: []PackageSnapshot{
			{
				PackageID: "com.sap.pkg1",
				Artifacts: []ArtifactSnapshot{
					{
						ID:   "iflow_001",
						Name: "My IFlow",
						Type: "Integration Flow",
						Versions: map[uint]ArtifactVersionInfo{
							1: {
								DesignTimeVersion: "1.0.5",
								RuntimeVersion:    "1.0.5",
								RuntimeStatus:     "STARTED",
							},
							2: {
								DesignTimeVersion: "1.0.4",
								RuntimeVersion:    "1.0.4",
								RuntimeStatus:     "STARTED",
							},
							3: {
								DesignTimeVersion: "active",
								RuntimeVersion:    "",
								RuntimeStatus:     "",
								Error:             "not deployed",
							},
						},
					},
				},
			},
			{
				PackageID: "com.sap.pkg2",
				Artifacts: []ArtifactSnapshot{},
			},
		},
	}

	snap := VersionCompareSnapshot{
		DeliveryRuleID: 300,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now().Truncate(time.Microsecond),
		CompletedAt:    &completedAt,
		TriggeredBy:    "test-user",
		Data:           data,
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var loaded VersionCompareSnapshot
	if err := testDB.First(&loaded, snap.ID).Error; err != nil {
		t.Fatalf("First failed: %v", err)
	}

	// Verify top-level SnapshotData fields
	if loaded.Data.SourceTenantID != 1 {
		t.Errorf("SourceTenantID: got %d, want 1", loaded.Data.SourceTenantID)
	}
	if len(loaded.Data.ComparedTenants) != 2 {
		t.Fatalf("ComparedTenants length: got %d, want 2", len(loaded.Data.ComparedTenants))
	}
	if loaded.Data.ComparedTenants[0] != 2 || loaded.Data.ComparedTenants[1] != 3 {
		t.Errorf("ComparedTenants: got %v, want [2 3]", loaded.Data.ComparedTenants)
	}

	// Verify packages
	if len(loaded.Data.Packages) != 2 {
		t.Fatalf("Packages length: got %d, want 2", len(loaded.Data.Packages))
	}

	pkg1 := loaded.Data.Packages[0]
	if pkg1.PackageID != "com.sap.pkg1" {
		t.Errorf("Package[0].PackageID: got %q, want %q", pkg1.PackageID, "com.sap.pkg1")
	}
	if len(pkg1.Artifacts) != 1 {
		t.Fatalf("Package[0].Artifacts length: got %d, want 1", len(pkg1.Artifacts))
	}

	art := pkg1.Artifacts[0]
	if art.ID != "iflow_001" {
		t.Errorf("Artifact.ID: got %q, want %q", art.ID, "iflow_001")
	}
	if art.Name != "My IFlow" {
		t.Errorf("Artifact.Name: got %q, want %q", art.Name, "My IFlow")
	}
	if art.Type != "Integration Flow" {
		t.Errorf("Artifact.Type: got %q, want %q", art.Type, "Integration Flow")
	}
	if len(art.Versions) != 3 {
		t.Fatalf("Artifact.Versions length: got %d, want 3", len(art.Versions))
	}

	// Tenant 1 (source)
	v1, ok := art.Versions[1]
	if !ok {
		t.Fatal("Versions[1] not found")
	}
	if v1.DesignTimeVersion != "1.0.5" {
		t.Errorf("Versions[1].DesignTimeVersion: got %q, want %q", v1.DesignTimeVersion, "1.0.5")
	}
	if v1.RuntimeVersion != "1.0.5" {
		t.Errorf("Versions[1].RuntimeVersion: got %q, want %q", v1.RuntimeVersion, "1.0.5")
	}
	if v1.RuntimeStatus != "STARTED" {
		t.Errorf("Versions[1].RuntimeStatus: got %q, want %q", v1.RuntimeStatus, "STARTED")
	}

	// Tenant 3 (with error)
	v3, ok := art.Versions[3]
	if !ok {
		t.Fatal("Versions[3] not found")
	}
	if v3.DesignTimeVersion != "active" {
		t.Errorf("Versions[3].DesignTimeVersion: got %q, want %q", v3.DesignTimeVersion, "active")
	}
	if v3.Error != "not deployed" {
		t.Errorf("Versions[3].Error: got %q, want %q", v3.Error, "not deployed")
	}

	// Empty package
	pkg2 := loaded.Data.Packages[1]
	if pkg2.PackageID != "com.sap.pkg2" {
		t.Errorf("Package[1].PackageID: got %q, want %q", pkg2.PackageID, "com.sap.pkg2")
	}
	if len(pkg2.Artifacts) != 0 {
		t.Errorf("Package[1].Artifacts: expected empty, got %d", len(pkg2.Artifacts))
	}

	// Verify CompletedAt
	if loaded.CompletedAt == nil {
		t.Fatal("CompletedAt: expected non-nil")
	}
	if !loaded.CompletedAt.Equal(completedAt) {
		t.Errorf("CompletedAt: got %v, want %v", loaded.CompletedAt, completedAt)
	}
}

// --- Empty SnapshotData ---

func TestVersionCompareSnapshot_EmptyData(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 400)

	snap := VersionCompareSnapshot{
		DeliveryRuleID: 400,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user",
		Data:           SnapshotData{},
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var loaded VersionCompareSnapshot
	if err := testDB.First(&loaded, snap.ID).Error; err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if loaded.Data.SourceTenantID != 0 {
		t.Errorf("SourceTenantID: got %d, want 0", loaded.Data.SourceTenantID)
	}
	if loaded.Data.ComparedTenants != nil && len(loaded.Data.ComparedTenants) != 0 {
		t.Errorf("ComparedTenants: expected nil/empty, got %v", loaded.Data.ComparedTenants)
	}
	if loaded.Data.Packages != nil && len(loaded.Data.Packages) != 0 {
		t.Errorf("Packages: expected nil/empty, got %v", loaded.Data.Packages)
	}
}

// --- Update (status transition running → completed) ---

func TestVersionCompareSnapshot_StatusTransition(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 500)

	snap := VersionCompareSnapshot{
		DeliveryRuleID: 500,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now().Truncate(time.Microsecond),
		TriggeredBy:    "trigger-user",
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Simulate completion
	completedAt := time.Now().Truncate(time.Microsecond)
	data := SnapshotData{
		SourceTenantID:  10,
		ComparedTenants: []uint{20},
		Packages: []PackageSnapshot{
			{PackageID: "pkg1", Artifacts: []ArtifactSnapshot{}},
		},
	}
	result := testDB.Model(&VersionCompareSnapshot{}).
		Where(&VersionCompareSnapshot{DeliveryRuleID: 500}).
		Select("Status", "CompletedAt", "Data").
		Updates(VersionCompareSnapshot{
			Status:      consts.SnapshotStatusCompleted,
			CompletedAt: &completedAt,
			Data:        data,
		})
	if result.Error != nil {
		t.Fatalf("Update failed: %v", result.Error)
	}
	if result.RowsAffected != 1 {
		t.Errorf("RowsAffected: got %d, want 1", result.RowsAffected)
	}

	var loaded VersionCompareSnapshot
	if err := testDB.Where(&VersionCompareSnapshot{DeliveryRuleID: 500}).First(&loaded).Error; err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if loaded.Status != consts.SnapshotStatusCompleted {
		t.Errorf("Status: got %q, want %q", loaded.Status, consts.SnapshotStatusCompleted)
	}
	if loaded.CompletedAt == nil {
		t.Fatal("CompletedAt: expected non-nil")
	}
	if !loaded.CompletedAt.Equal(completedAt) {
		t.Errorf("CompletedAt: got %v, want %v", loaded.CompletedAt, completedAt)
	}
	if loaded.Data.SourceTenantID != 10 {
		t.Errorf("Data.SourceTenantID: got %d, want 10", loaded.Data.SourceTenantID)
	}
}

// --- Atomic Concurrent Protection (UPDATE WHERE status != 'running') ---

func TestVersionCompareSnapshot_AtomicConcurrentProtection(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 600)

	// Create a snapshot in "running" state
	snap := VersionCompareSnapshot{
		DeliveryRuleID: 600,
		Status:         consts.SnapshotStatusRunning,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user1",
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to update where status != 'running' — should affect 0 rows
	result := testDB.Model(&VersionCompareSnapshot{}).
		Where(&VersionCompareSnapshot{DeliveryRuleID: 600}).
		Where("status != ?", consts.SnapshotStatusRunning).
		Select("Status", "TriggeredAt", "TriggeredBy").
		Updates(VersionCompareSnapshot{
			Status:      consts.SnapshotStatusRunning,
			TriggeredAt: time.Now(),
			TriggeredBy: "user2",
		})
	if result.Error != nil {
		t.Fatalf("Update failed: %v", result.Error)
	}
	if result.RowsAffected != 0 {
		t.Errorf("RowsAffected: got %d, want 0 (concurrent protection should reject)", result.RowsAffected)
	}

	// Complete the first snapshot
	completedAt := time.Now()
	testDB.Model(&VersionCompareSnapshot{}).
		Where(&VersionCompareSnapshot{DeliveryRuleID: 600}).
		Select("Status", "CompletedAt").
		Updates(VersionCompareSnapshot{
			Status:      consts.SnapshotStatusCompleted,
			CompletedAt: &completedAt,
		})

	// Now retry — should succeed since status is "completed"
	result = testDB.Model(&VersionCompareSnapshot{}).
		Where(&VersionCompareSnapshot{DeliveryRuleID: 600}).
		Where("status != ?", consts.SnapshotStatusRunning).
		Select("Status", "TriggeredAt", "TriggeredBy").
		Updates(VersionCompareSnapshot{
			Status:      consts.SnapshotStatusRunning,
			TriggeredAt: time.Now(),
			TriggeredBy: "user2",
		})
	if result.Error != nil {
		t.Fatalf("Retry Update failed: %v", result.Error)
	}
	if result.RowsAffected != 1 {
		t.Errorf("Retry RowsAffected: got %d, want 1", result.RowsAffected)
	}
}

// --- Failed status with error message ---

func TestVersionCompareSnapshot_FailedWithError(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 700)

	now := time.Now().Truncate(time.Microsecond)
	completedAt := now.Add(5 * time.Second).Truncate(time.Microsecond)

	snap := VersionCompareSnapshot{
		DeliveryRuleID: 700,
		Status:         consts.SnapshotStatusFailed,
		TriggeredAt:    now,
		CompletedAt:    &completedAt,
		TriggeredBy:    "user",
		Error:          "failed to get source tenant CPI client: tenant not found",
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var loaded VersionCompareSnapshot
	if err := testDB.First(&loaded, snap.ID).Error; err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if loaded.Status != consts.SnapshotStatusFailed {
		t.Errorf("Status: got %q, want %q", loaded.Status, consts.SnapshotStatusFailed)
	}
	if loaded.Error != "failed to get source tenant CPI client: tenant not found" {
		t.Errorf("Error: got %q, want full error message", loaded.Error)
	}
}

// --- Lookup by DeliveryRuleID (the primary query pattern) ---

func TestVersionCompareSnapshot_LookupByDeliveryRuleID(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 801, 802)

	// Create snapshots for two different rules
	for _, ruleID := range []uint{801, 802} {
		snap := VersionCompareSnapshot{
			DeliveryRuleID: ruleID,
			Status:         consts.SnapshotStatusCompleted,
			TriggeredAt:    time.Now(),
			TriggeredBy:    "user",
			Data: SnapshotData{
				SourceTenantID:  ruleID * 10,
				ComparedTenants: []uint{ruleID*10 + 1},
			},
		}
		if err := testDB.Create(&snap).Error; err != nil {
			t.Fatalf("Create for rule %d failed: %v", ruleID, err)
		}
	}

	// Look up rule 801
	var snap801 VersionCompareSnapshot
	if err := testDB.Where(&VersionCompareSnapshot{DeliveryRuleID: 801}).First(&snap801).Error; err != nil {
		t.Fatalf("lookup 801 failed: %v", err)
	}
	if snap801.Data.SourceTenantID != 8010 {
		t.Errorf("rule 801 SourceTenantID: got %d, want 8010", snap801.Data.SourceTenantID)
	}

	// Look up nonexistent rule
	var snapMissing VersionCompareSnapshot
	err := testDB.Where(&VersionCompareSnapshot{DeliveryRuleID: 999}).First(&snapMissing).Error
	if err == nil {
		t.Fatal("expected error for nonexistent rule, got nil")
	}
}

// --- Upsert pattern: data overwrite on re-trigger ---

func TestVersionCompareSnapshot_DataOverwriteOnRetrigger(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 900)

	// First trigger — create with initial data
	data1 := SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2},
		Packages: []PackageSnapshot{
			{
				PackageID: "old-pkg",
				Artifacts: []ArtifactSnapshot{
					{
						ID:   "art1",
						Name: "Artifact 1",
						Type: "Integration Flow",
						Versions: map[uint]ArtifactVersionInfo{
							1: {DesignTimeVersion: "1.0.0", RuntimeVersion: "1.0.0", RuntimeStatus: "STARTED"},
						},
					},
				},
			},
		},
	}
	completedAt1 := time.Now().Truncate(time.Microsecond)
	snap := VersionCompareSnapshot{
		DeliveryRuleID: 900,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now().Truncate(time.Microsecond),
		CompletedAt:    &completedAt1,
		TriggeredBy:    "user1",
		Data:           data1,
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Second trigger — simulate the re-trigger flow: reset to running, then complete with new data
	result := testDB.Model(&VersionCompareSnapshot{}).
		Where(&VersionCompareSnapshot{DeliveryRuleID: 900}).
		Where("status != ?", consts.SnapshotStatusRunning).
		Select("Status", "TriggeredAt", "TriggeredBy", "CompletedAt", "Error", "Data").
		Updates(VersionCompareSnapshot{
			Status:      consts.SnapshotStatusRunning,
			TriggeredAt: time.Now(),
			TriggeredBy: "user2",
			CompletedAt: nil,
			Error:       "",
			Data:        SnapshotData{},
		})
	if result.Error != nil {
		t.Fatalf("reset to running failed: %v", result.Error)
	}

	// Complete with new data
	data2 := SnapshotData{
		SourceTenantID:  1,
		ComparedTenants: []uint{2, 3},
		Packages: []PackageSnapshot{
			{
				PackageID: "new-pkg",
				Artifacts: []ArtifactSnapshot{
					{
						ID:   "art2",
						Name: "Artifact 2",
						Type: "Script Collection",
						Versions: map[uint]ArtifactVersionInfo{
							1: {DesignTimeVersion: "2.0.0", RuntimeVersion: "2.0.0", RuntimeStatus: "STARTED"},
							2: {DesignTimeVersion: "1.9.0", RuntimeVersion: "1.9.0", RuntimeStatus: "ERROR"},
						},
					},
				},
			},
		},
	}
	completedAt2 := time.Now().Truncate(time.Microsecond)
	testDB.Model(&VersionCompareSnapshot{}).
		Where(&VersionCompareSnapshot{DeliveryRuleID: 900}).
		Select("Status", "CompletedAt", "Data").
		Updates(VersionCompareSnapshot{
			Status:      consts.SnapshotStatusCompleted,
			CompletedAt: &completedAt2,
			Data:        data2,
		})

	// Verify old data is fully replaced
	var loaded VersionCompareSnapshot
	if err := testDB.Where(&VersionCompareSnapshot{DeliveryRuleID: 900}).First(&loaded).Error; err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if loaded.TriggeredBy != "user2" {
		t.Errorf("TriggeredBy: got %q, want %q", loaded.TriggeredBy, "user2")
	}
	if len(loaded.Data.ComparedTenants) != 2 {
		t.Fatalf("ComparedTenants length: got %d, want 2", len(loaded.Data.ComparedTenants))
	}
	if len(loaded.Data.Packages) != 1 {
		t.Fatalf("Packages length: got %d, want 1", len(loaded.Data.Packages))
	}
	if loaded.Data.Packages[0].PackageID != "new-pkg" {
		t.Errorf("PackageID: got %q, want %q", loaded.Data.Packages[0].PackageID, "new-pkg")
	}
	if len(loaded.Data.Packages[0].Artifacts) != 1 {
		t.Fatalf("Artifacts length: got %d, want 1", len(loaded.Data.Packages[0].Artifacts))
	}
	art := loaded.Data.Packages[0].Artifacts[0]
	if art.ID != "art2" {
		t.Errorf("Artifact.ID: got %q, want %q", art.ID, "art2")
	}
	if art.Type != "Script Collection" {
		t.Errorf("Artifact.Type: got %q, want %q", art.Type, "Script Collection")
	}
	v2, ok := art.Versions[2]
	if !ok {
		t.Fatal("Versions[2] not found")
	}
	if v2.RuntimeStatus != "ERROR" {
		t.Errorf("Versions[2].RuntimeStatus: got %q, want %q", v2.RuntimeStatus, "ERROR")
	}
}

// --- Large snapshot with many packages/artifacts ---

func TestVersionCompareSnapshot_LargePayload(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 1000)

	// Build a snapshot with 20 packages, 50 artifacts each, 5 tenants
	packages := make([]PackageSnapshot, 20)
	for p := 0; p < 20; p++ {
		artifacts := make([]ArtifactSnapshot, 50)
		for a := 0; a < 50; a++ {
			versions := make(map[uint]ArtifactVersionInfo, 5)
			for tid := uint(1); tid <= 5; tid++ {
				versions[tid] = ArtifactVersionInfo{
					DesignTimeVersion: "1.0.0",
					RuntimeVersion:    "1.0.0",
					RuntimeStatus:     "STARTED",
				}
			}
			artifacts[a] = ArtifactSnapshot{
				ID:       fmt.Sprintf("art_%d_%d", p, a),
				Name:     fmt.Sprintf("Artifact %d-%d", p, a),
				Type:     "Integration Flow",
				Versions: versions,
			}
		}
		packages[p] = PackageSnapshot{
			PackageID: fmt.Sprintf("com.sap.pkg%d", p),
			Artifacts: artifacts,
		}
	}

	snap := VersionCompareSnapshot{
		DeliveryRuleID: 1000,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user",
		Data: SnapshotData{
			SourceTenantID:  1,
			ComparedTenants: []uint{2, 3, 4, 5},
			Packages:        packages,
		},
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var loaded VersionCompareSnapshot
	if err := testDB.First(&loaded, snap.ID).Error; err != nil {
		t.Fatalf("First failed: %v", err)
	}
	if len(loaded.Data.Packages) != 20 {
		t.Errorf("Packages: got %d, want 20", len(loaded.Data.Packages))
	}
	if len(loaded.Data.Packages[0].Artifacts) != 50 {
		t.Errorf("Artifacts[0]: got %d, want 50", len(loaded.Data.Packages[0].Artifacts))
	}
	if len(loaded.Data.Packages[19].Artifacts[49].Versions) != 5 {
		t.Errorf("Versions: got %d, want 5", len(loaded.Data.Packages[19].Artifacts[49].Versions))
	}
}

// --- GORM soft delete: deleted snapshot should not appear in normal queries ---

func TestVersionCompareSnapshot_SoftDelete(t *testing.T) {
	cleanSnapshotsByRuleIDs(t, 1100)

	snap := VersionCompareSnapshot{
		DeliveryRuleID: 1100,
		Status:         consts.SnapshotStatusCompleted,
		TriggeredAt:    time.Now(),
		TriggeredBy:    "user",
	}
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Soft delete
	if err := testDB.Delete(&snap).Error; err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Normal query should not find it
	var loaded VersionCompareSnapshot
	err := testDB.Where(&VersionCompareSnapshot{DeliveryRuleID: 1100}).First(&loaded).Error
	if err == nil {
		t.Fatal("expected not-found after soft delete, got nil")
	}

	// Unscoped query should still find it
	err = testDB.Unscoped().Where(&VersionCompareSnapshot{DeliveryRuleID: 1100}).First(&loaded).Error
	if err != nil {
		t.Fatalf("Unscoped query failed: %v", err)
	}
	if loaded.DeliveryRuleID != 1100 {
		t.Errorf("DeliveryRuleID: got %d, want 1100", loaded.DeliveryRuleID)
	}
}
