package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB is the shared *gorm.DB for service-layer tests.
var testDB *gorm.DB

func TestMain(m *testing.M) {
	uri := os.Getenv("LOCAL_POSTGRES_URI")
	if uri == "" {
		fmt.Println("SKIP: LOCAL_POSTGRES_URI not set, skipping service tests")
		os.Exit(0)
	}

	conn, err := sql.Open("pgx", uri)
	if err != nil {
		fmt.Printf("FATAL: failed to open database: %v\n", err)
		os.Exit(1)
	}

	testDB, err = gorm.Open(
		postgres.New(postgres.Config{Conn: conn}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		fmt.Printf("FATAL: failed to initialize gorm: %v\n", err)
		os.Exit(1)
	}

	// Migrate models needed by service-layer version compare tests.
	if err := testDB.AutoMigrate(
		&db.CpiTenant{},
		&db.DeliveryRule{},
		&db.VersionCompareSnapshot{},
		&db.VersionCompareIncludedPackage{},
	); err != nil {
		fmt.Printf("FATAL: failed to migrate: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	os.Exit(code)
}

// newTestService creates a Service wired to testDB with a given mock CPI factory.
func newTestService(factory IntegrationFactory) *Service {
	l, _ := zap.NewDevelopment()
	return &Service{
		DB:     testDB,
		Logger: l.Sugar(),
		CPI:    factory,
	}
}

// --- testCleanup tracks IDs created during a test and deletes them on t.Cleanup ---

type testCleanup struct {
	t                 *testing.T
	tenantIDs         []uint
	ruleIDs           []uint
	cleanIncludedPkgs bool // whether to clean VersionCompareIncludedPackage table
}

// newTestCleanup registers a t.Cleanup that deletes all tracked records
// in the correct order (snapshots → rules → tenants) to respect FK constraints.
// It uses Unscoped to also remove soft-deleted records.
func newTestCleanup(t *testing.T) *testCleanup {
	t.Helper()
	tc := &testCleanup{t: t}
	t.Cleanup(func() {
		// Delete included packages if flagged
		if tc.cleanIncludedPkgs {
			testDB.Unscoped().Where("1 = 1").Delete(&db.VersionCompareIncludedPackage{})
		}
		// Delete snapshots for tracked rules
		if len(tc.ruleIDs) > 0 {
			testDB.Unscoped().Where("delivery_rule_id IN ?", tc.ruleIDs).Delete(&db.VersionCompareSnapshot{})
		}
		// Delete rules (also clears the delivery_rule_included_tenants join table via GORM association)
		if len(tc.ruleIDs) > 0 {
			for _, id := range tc.ruleIDs {
				var rule db.DeliveryRule
				rule.ID = id
				testDB.Model(&rule).Association("IncludedTenants").Clear()
			}
			testDB.Unscoped().Where("id IN ?", tc.ruleIDs).Delete(&db.DeliveryRule{})
		}
		// Delete tenants
		if len(tc.tenantIDs) > 0 {
			testDB.Unscoped().Where("id IN ?", tc.tenantIDs).Delete(&db.CpiTenant{})
		}
	})
	return tc
}

func (tc *testCleanup) trackTenant(id uint) { tc.tenantIDs = append(tc.tenantIDs, id) }
func (tc *testCleanup) trackRule(id uint)   { tc.ruleIDs = append(tc.ruleIDs, id) }

// --- Mock CPI Client ---

// mockCPIClient implements IntegrationService for testing.
type mockCPIClient struct {
	packages       []cpi.CPIPackage
	packagesErr    error
	iflows         map[string][]cpi.IflowItem // key = packageID
	iflowsErr      map[string]error
	scriptColls    map[string][]cpi.ScriptCollectionItem // key = packageID
	scriptCollsErr map[string]error
	runtimeArts    []cpi.RuntimeArtifact
	runtimeArtsErr error
}

func (m *mockCPIClient) GetPackages(ctx context.Context) ([]cpi.CPIPackage, error) {
	return m.packages, m.packagesErr
}
func (m *mockCPIClient) GetPackageIflows(ctx context.Context, packageID string) ([]cpi.IflowItem, error) {
	if m.iflowsErr != nil {
		if e, ok := m.iflowsErr[packageID]; ok {
			return nil, e
		}
	}
	return m.iflows[packageID], nil
}
func (m *mockCPIClient) GetPackageScriptcollections(ctx context.Context, packageID string) ([]cpi.ScriptCollectionItem, error) {
	if m.scriptCollsErr != nil {
		if e, ok := m.scriptCollsErr[packageID]; ok {
			return nil, e
		}
	}
	return m.scriptColls[packageID], nil
}
func (m *mockCPIClient) GetRuntimeArtifacts(ctx context.Context) ([]cpi.RuntimeArtifact, error) {
	return m.runtimeArts, m.runtimeArtsErr
}
func (m *mockCPIClient) DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error) {
	return "", nil
}
func (m *mockCPIClient) RuntimeArtifact(ctx context.Context, artifactId string) (cpi.RuntimeArtifact, error) {
	return cpi.RuntimeArtifact{}, nil
}
func (m *mockCPIClient) GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (cpi.IflowItem, error) {
	return cpi.IflowItem{}, nil
}
func (m *mockCPIClient) GetDesignTimeScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) (cpi.ScriptCollectionItem, error) {
	return cpi.ScriptCollectionItem{}, nil
}

// --- Seed Helpers ---

// seedTenant creates a CpiTenant in the test DB, tracks it for cleanup, and returns it.
func seedTenant(t *testing.T, tc *testCleanup, name string) db.CpiTenant {
	t.Helper()
	tenant := db.CpiTenant{
		Name: name,
		CpiEndpoint: db.ApiEndpoint{
			Name: name, // use tenant name as endpoint key
		},
	}
	if err := testDB.Create(&tenant).Error; err != nil {
		t.Fatalf("seedTenant(%s) failed: %v", name, err)
	}
	tc.trackTenant(tenant.ID)
	return tenant
}

// seedRule creates a DeliveryRule, tracks it for cleanup, and returns it.
func seedRule(t *testing.T, tc *testCleanup, name string, source db.CpiTenant, included []db.CpiTenant, active bool) db.DeliveryRule {
	t.Helper()
	rule := db.DeliveryRule{
		Name:            name,
		SourceTenantID:  source.ID,
		IncludedTenants: included,
		Active:          active,
		VersionPattern:  "*",
	}
	if err := testDB.Create(&rule).Error; err != nil {
		t.Fatalf("seedRule(%s) failed: %v", name, err)
	}
	tc.trackRule(rule.ID)
	return rule
}

// seedSnapshot creates a VersionCompareSnapshot directly in the DB.
// The snapshot's DeliveryRuleID should belong to a rule already tracked by tc.
func seedSnapshot(t *testing.T, snap db.VersionCompareSnapshot) db.VersionCompareSnapshot {
	t.Helper()
	if err := testDB.Create(&snap).Error; err != nil {
		t.Fatalf("seedSnapshot failed: %v", err)
	}
	return snap
}

// waitForSnapshotComplete polls the DB until the snapshot for ruleID reaches a terminal
// status (completed or failed) or the timeout expires.
func waitForSnapshotComplete(t *testing.T, ruleID uint, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var snap db.VersionCompareSnapshot
		if err := testDB.Where(&db.VersionCompareSnapshot{DeliveryRuleID: ruleID}).First(&snap).Error; err == nil {
			if snap.Status == consts.SnapshotStatusCompleted || snap.Status == consts.SnapshotStatusFailed {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("snapshot for rule %d did not reach terminal status within %v", ruleID, timeout)
}
