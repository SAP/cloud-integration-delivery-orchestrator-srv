package db

import (
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=private"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.AutoMigrate(&CpiTenant{}, &TenantBootstrapJob{}, &DataMigration{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func TestP0TmsStateBackfill(t *testing.T) {
	database := setupTestDB(t)

	// Seed tenants covering every backfill branch.
	tenants := []CpiTenant{
		{Name: "ready-tms", CfApiEndpoint: "https://a", CfOrg: "org1", LifecycleState: lifecycle.TenantReady, TmsNodeRegistrationStatus: lifecycle.PrereqReady},
		{Name: "configured-tms", CfApiEndpoint: "https://a", CfOrg: "org2", LifecycleState: lifecycle.TenantConfigured, TmsNodeRegistrationStatus: lifecycle.PrereqReady},
		{Name: "registering", CfApiEndpoint: "https://a", CfOrg: "org3", LifecycleState: lifecycle.TenantConfigured, TmsNodeRegistrationStatus: lifecycle.PrereqRegistering},
		{Name: "no-tms", CfApiEndpoint: "https://a", CfOrg: "org4", LifecycleState: lifecycle.TenantReady, TmsNodeRegistrationStatus: lifecycle.PrereqMissing},
		{Name: "draft-no-tms", CfApiEndpoint: "https://a", CfOrg: "org5", LifecycleState: lifecycle.TenantDraft, TmsNodeRegistrationStatus: lifecycle.PrereqMissing},
	}
	for i := range tenants {
		if err := database.Create(&tenants[i]).Error; err != nil {
			t.Fatalf("create tenant %q: %v", tenants[i].Name, err)
		}
	}

	// Run backfill.
	if err := p0TmsStateBackfill(database); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	// Verify results.
	expect := map[string]lifecycle.TmsExtensionState{
		"ready-tms":      lifecycle.TmsBootstrapped,
		"configured-tms": lifecycle.TmsNodeConfirmed,
		"registering":    lifecycle.TmsNodeConfirmed,
		"no-tms":         lifecycle.TmsNotStarted,
		"draft-no-tms":   lifecycle.TmsNotStarted,
	}
	for name, want := range expect {
		var tenant CpiTenant
		if err := database.Where("name = ?", name).First(&tenant).Error; err != nil {
			t.Fatalf("load %q: %v", name, err)
		}
		if tenant.TmsExtensionState != want {
			t.Errorf("tenant %q: TmsExtensionState = %q, want %q", name, tenant.TmsExtensionState, want)
		}
	}
}

func TestP0TmsStateBackfill_Idempotent(t *testing.T) {
	database := setupTestDB(t)

	tenant := CpiTenant{
		Name:                      "idempotent",
		LifecycleState:            lifecycle.TenantReady,
		TmsNodeRegistrationStatus: lifecycle.PrereqReady,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	// Run twice.
	for i := 0; i < 2; i++ {
		if err := p0TmsStateBackfill(database); err != nil {
			t.Fatalf("backfill run %d: %v", i+1, err)
		}
	}

	var got CpiTenant
	if err := database.First(&got, tenant.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.TmsExtensionState != lifecycle.TmsBootstrapped {
		t.Errorf("TmsExtensionState = %q after double run, want %q", got.TmsExtensionState, lifecycle.TmsBootstrapped)
	}
}

func TestRunDataMigrations_SkipsAlreadyApplied(t *testing.T) {
	database := setupTestDB(t)

	// First run should apply.
	if err := RunDataMigrations(database); err != nil {
		t.Fatalf("first run: %v", err)
	}

	var count int64
	database.Model(&DataMigration{}).Count(&count)
	if count == 0 {
		t.Fatal("expected at least one migration record")
	}

	// Second run should be a no-op.
	if err := RunDataMigrations(database); err != nil {
		t.Fatalf("second run: %v", err)
	}

	var count2 int64
	database.Model(&DataMigration{}).Count(&count2)
	if count2 != count {
		t.Errorf("migration count changed: %d → %d", count, count2)
	}
}

func TestCpiTenant_IsOnboarded(t *testing.T) {
	tests := []struct {
		name   string
		tenant CpiTenant
		want   bool
	}{
		{"ready with dest", CpiTenant{LifecycleState: lifecycle.TenantReady, PirApiDestinationName: "CPIDELIVERY_PIR_1"}, true},
		{"ready no dest", CpiTenant{LifecycleState: lifecycle.TenantReady}, true},
		{"not ready with dest", CpiTenant{LifecycleState: lifecycle.TenantConfigured, PirApiDestinationName: "CPIDELIVERY_PIR_1"}, false},
		{"draft", CpiTenant{LifecycleState: lifecycle.TenantDraft}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tenant.IsOnboarded(); got != tt.want {
				t.Errorf("IsOnboarded() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCpiTenant_IsTmsReady(t *testing.T) {
	tests := []struct {
		name   string
		tenant CpiTenant
		want   bool
	}{
		{"ready", CpiTenant{TmsExtensionState: lifecycle.TmsBootstrapped}, true},
		{"not started", CpiTenant{TmsExtensionState: lifecycle.TmsNotStarted}, false},
		{"confirmed", CpiTenant{TmsExtensionState: lifecycle.TmsNodeConfirmed}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tenant.IsTmsReady(); got != tt.want {
				t.Errorf("IsTmsReady() = %v, want %v", got, tt.want)
			}
		})
	}
}
