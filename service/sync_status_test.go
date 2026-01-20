package service

import (
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestConcurrentSyncImportState tests the concurrent scenario where multiple
// requests call syncImportState at the same time, trying to create the same
// ArtifactTenantOperation records
func TestConcurrentSyncImportState(t *testing.T) {
	// Connect to test database - adjust DSN as needed
	dsn := "host=localhost user=postgres password=postgres dbname=test_db port=5432 sslmode=disable"
	testDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("Skipping integration test: cannot connect to test database: %v", err)
		return
	}

	// Setup test data
	tenant := db.CpiTenant{
		Name: "test_tenant_concurrent",
	}
	if err := testDB.FirstOrCreate(&tenant, "name = ?", tenant.Name).Error; err != nil {
		t.Fatalf("Failed to create test tenant: %v", err)
	}
	defer testDB.Delete(&tenant)

	artifact := db.Artifact{
		Name:    "test-artifact",
		Version: "1.0.0",
		Type:    "iflow",
	}
	if err := testDB.FirstOrCreate(&artifact, "name = ? AND version = ?", artifact.Name, artifact.Version).Error; err != nil {
		t.Fatalf("Failed to create test artifact: %v", err)
	}
	defer testDB.Delete(&artifact)

	deliveryRequest := db.DeliveryRequest{
		Name:            "test-dr-concurrent",
		SourceTenantID:  tenant.ID,
		AggregateStatus: lifecycle.AggPending,
	}
	if err := testDB.Create(&deliveryRequest).Error; err != nil {
		t.Fatalf("Failed to create delivery request: %v", err)
	}
	defer testDB.Delete(&deliveryRequest)

	trNumber := "TEST_CONCURRENT_TR_001"

	// Number of concurrent requests
	numGoroutines := 5

	var wg sync.WaitGroup
	results := make([]error, numGoroutines)

	// Simulate concurrent requests calling syncImportState
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// Each goroutine tries to create the same operation
			op := db.ArtifactTenantOperation{
				DeliveryRequestID:      deliveryRequest.ID,
				ArtifactID:             artifact.ID,
				ArtifactTechID:         artifact.Name,
				ArtifactVersion:        artifact.Version,
				TenantID:               tenant.ID,
				TransportRequestNumber: trNumber,
				ImportState:            lifecycle.ImportNotStarted,
				DeployState:            lifecycle.DeployNotStarted,
				CreatedBy:              fmt.Sprintf("user-%d", index),
			}

			// This simulates what happens in syncImportState
			err := testDB.Save(&op).Error
			results[index] = err
		}(i)
	}

	wg.Wait()

	// Analyze results
	successCount := 0
	duplicateErrorCount := 0
	otherErrorCount := 0

	for i, err := range results {
		if err == nil {
			successCount++
			t.Logf("Goroutine %d: SUCCESS", i)
		} else if isDuplicateKeyError(err) {
			duplicateErrorCount++
			t.Logf("Goroutine %d: DUPLICATE ERROR (expected): %v", i, err)
		} else {
			otherErrorCount++
			t.Logf("Goroutine %d: OTHER ERROR (unexpected): %v", i, err)
		}
	}

	// Verify: exactly one record should be created
	var count int64
	testDB.Model(&db.ArtifactTenantOperation{}).
		Where("tenant_id = ? AND transport_request_number = ?", tenant.ID, trNumber).
		Count(&count)

	t.Logf("\n=== Test Results ===")
	t.Logf("Total goroutines: %d", numGoroutines)
	t.Logf("Success count: %d", successCount)
	t.Logf("Duplicate key errors: %d", duplicateErrorCount)
	t.Logf("Other errors: %d", otherErrorCount)
	t.Logf("Records in database: %d", count)

	// Assertions
	if count != 1 {
		t.Errorf("FAILED: Expected exactly 1 record, but found %d", count)
	}

	if otherErrorCount > 0 {
		t.Errorf("FAILED: Found %d unexpected errors", otherErrorCount)
	}

	// Verify that successCount + duplicateErrorCount equals numGoroutines
	if successCount+duplicateErrorCount != numGoroutines {
		t.Errorf("FAILED: successCount + duplicateErrorCount != numGoroutines (%d + %d != %d)",
			successCount, duplicateErrorCount, numGoroutines)
	}

	// Clean up
	testDB.Where("transport_request_number = ?", trNumber).Delete(&db.ArtifactTenantOperation{})
}

// TestConcurrentSyncImportStateWithVaryingParams tests concurrent creation
// with different (tenant_id, tr_number) combinations
func TestConcurrentSyncImportStateWithVaryingParams(t *testing.T) {
	dsn := "host=localhost user=postgres password=postgres dbname=test_db port=5432 sslmode=disable"
	testDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Skipf("Skipping integration test: cannot connect to test database: %v", err)
		return
	}

	// Create multiple tenants
	tenants := make([]db.CpiTenant, 3)
	for i := 0; i < 3; i++ {
		tenants[i] = db.CpiTenant{
			Name: fmt.Sprintf("test-tenant-%d", i),
		}
		if err := testDB.FirstOrCreate(&tenants[i], "name = ?", tenants[i].Name).Error; err != nil {
			t.Fatalf("Failed to create tenant %d: %v", i, err)
		}
		defer testDB.Delete(&tenants[i])
	}

	artifact := db.Artifact{
		Name:    "test-artifact-multi",
		Version: "1.0.0",
		Type:    "iflow",
	}
	if err := testDB.FirstOrCreate(&artifact, "name = ? AND version = ?", artifact.Name, artifact.Version).Error; err != nil {
		t.Fatalf("Failed to create test artifact: %v", err)
	}
	defer testDB.Delete(&artifact)

	deliveryRequest := db.DeliveryRequest{
		Name:            "test-dr-multi",
		AggregateStatus: lifecycle.AggPending,
	}
	if err := testDB.Create(&deliveryRequest).Error; err != nil {
		t.Fatalf("Failed to create delivery request: %v", err)
	}
	defer testDB.Delete(&deliveryRequest)

	trNumber := "TEST_MULTI_TR_001"

	var wg sync.WaitGroup

	// Each tenant will be accessed by 3 concurrent goroutines
	for tenantIdx := 0; tenantIdx < 3; tenantIdx++ {
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(tIdx int, gIdx int) {
				defer wg.Done()

				op := db.ArtifactTenantOperation{
					DeliveryRequestID:      deliveryRequest.ID,
					ArtifactID:             artifact.ID,
					ArtifactTechID:         artifact.Name,
					ArtifactVersion:        artifact.Version,
					TenantID:               tenants[tIdx].ID,
					TransportRequestNumber: trNumber,
					ImportState:            lifecycle.ImportNotStarted,
					DeployState:            lifecycle.DeployNotStarted,
					CreatedBy:              fmt.Sprintf("user-%d-%d", tIdx, gIdx),
				}

				_ = testDB.Save(&op).Error
			}(tenantIdx, i)
		}
	}

	wg.Wait()

	// Verify: 3 records should be created (one per tenant)
	var count int64
	testDB.Model(&db.ArtifactTenantOperation{}).
		Where("transport_request_number = ?", trNumber).
		Count(&count)

	t.Logf("\n=== Multi-Tenant Test Results ===")
	t.Logf("Total goroutines: 9 (3 tenants x 3 concurrent)")
	t.Logf("Records created: %d", count)

	if count != 3 {
		t.Errorf("FAILED: Expected 3 records (one per tenant), but found %d", count)
	}

	// Clean up
	testDB.Where("transport_request_number = ?", trNumber).Delete(&db.ArtifactTenantOperation{})
}
