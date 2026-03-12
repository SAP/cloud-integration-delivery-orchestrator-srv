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

// cleanAll truncates all tables used by version compare tests.
// Call at the start of each test for isolation.
func cleanAll(t *testing.T) {
	t.Helper()
	// Use TRUNCATE CASCADE to handle FK dependencies from other tables
	// (e.g. delivery_requests → delivery_rules).
	for _, stmt := range []string{
		"TRUNCATE TABLE version_compare_snapshots CASCADE",
		"TRUNCATE TABLE delivery_rules CASCADE",
		"TRUNCATE TABLE cpi_tenants CASCADE",
	} {
		if err := testDB.Exec(stmt).Error; err != nil {
			// Table may not exist; log but don't fail.
			t.Logf("cleanAll: %s → %v", stmt, err)
		}
	}
}

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

// seedTenant creates a CpiTenant in the test DB and returns it with populated ID.
func seedTenant(t *testing.T, name string) db.CpiTenant {
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
	return tenant
}

// seedRule creates a DeliveryRule with the given source and included tenants.
func seedRule(t *testing.T, name string, source db.CpiTenant, included []db.CpiTenant, active bool) db.DeliveryRule {
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
	return rule
}

// seedSnapshot creates a VersionCompareSnapshot directly in the DB.
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
