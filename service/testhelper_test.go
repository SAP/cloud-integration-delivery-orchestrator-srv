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
	"mmt-delivery/pkg/cas"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/tms"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	var dialector gorm.Dialector

	if uri := os.Getenv("LOCAL_POSTGRES_URI"); uri != "" {
		conn, err := sql.Open("pgx", uri)
		if err != nil {
			fmt.Printf("FATAL: failed to open postgres: %v\n", err)
			os.Exit(1)
		}
		dialector = postgres.New(postgres.Config{Conn: conn})
		fmt.Fprintln(os.Stderr, "INFO: using PostgreSQL for tests")
	} else {
		dialector = sqlite.Open("file::memory:?cache=shared")
		fmt.Fprintln(os.Stderr, "INFO: using SQLite (in-memory) for tests")
	}

	var err error
	testDB, err = gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Printf("FATAL: failed to initialize gorm: %v\n", err)
		os.Exit(1)
	}

	if err := testDB.AutoMigrate(
		&db.CpiTenant{},
		&db.DeliveryRule{},
		&db.VersionCompareSnapshot{},
		&db.VersionCompareIncludedPackage{},
		&db.Artifact{},
		&db.DeliveryRequest{},
		&db.ArtifactTenantOperation{},
		&db.Condition{},
	); err != nil {
		fmt.Printf("FATAL: failed to migrate: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// testServiceOpts holds optional dependencies for building a test Service.
type testServiceOpts struct {
	tms          TransportService
	cas          CasService
	notifier     Notifier
	getUserEmail func(ctx context.Context, userID string) (string, error)
}

// newTestService creates a Service wired to testDB with a given mock CPI factory.
// It sets sensible defaults for TMS/Notifier/GetUserEmail (no-op mocks) so that
// existing tests that only care about CPI continue to work unchanged.
// Automatically skips the calling test when testDB is nil.
func newTestService(factory IntegrationFactory, opts ...testServiceOpts) *Service {
	l, _ := zap.NewDevelopment()
	svc := &Service{
		DB:     testDB,
		Logger: l.Sugar(),
		CPI:    factory,
		TmsSvc: func(ctx context.Context) (TransportService, error) {
			return nil, fmt.Errorf("TMS not configured in test")
		},
		CAS: func(ctx context.Context, tenantID uint) (CasService, error) {
			return nil, fmt.Errorf("CAS not configured in test")
		},
	}
	if len(opts) > 0 {
		o := opts[0]
		if o.tms != nil {
			svc.TmsSvc = func(ctx context.Context) (TransportService, error) {
				return o.tms, nil
			}
		}
		if o.cas != nil {
			svc.CAS = func(ctx context.Context, tenantID uint) (CasService, error) {
				return o.cas, nil
			}
		}
		if o.notifier != nil {
			svc.Notifier = o.notifier
		}
		if o.getUserEmail != nil {
			svc.GetUserEmail = o.getUserEmail
		}
	}
	// Fallback defaults so callers that don't need these don't crash
	if svc.Notifier == nil {
		svc.Notifier = &mockNotifier{}
	}
	if svc.GetUserEmail == nil {
		svc.GetUserEmail = func(ctx context.Context, userID string) (string, error) {
			return userID + "@test.com", nil
		}
	}
	return svc
}

// --- testCleanup tracks IDs created during a test and deletes them on t.Cleanup ---

type testCleanup struct {
	t                 *testing.T
	tenantIDs         []uint
	ruleIDs           []uint
	drIDs             []uint
	artifactIDs       []uint
	cleanIncludedPkgs bool // whether to clean VersionCompareIncludedPackage table
}

// newTestCleanup registers a t.Cleanup that deletes all tracked records
// in the correct order to respect FK constraints:
// conditions → ops → artifacts → DRs → snapshots → rules → tenants
// It uses Unscoped to also remove soft-deleted records.
func newTestCleanup(t *testing.T) *testCleanup {
	t.Helper()
	tc := &testCleanup{t: t}
	t.Cleanup(func() {
		// Delete included packages if flagged
		if tc.cleanIncludedPkgs {
			testDB.Unscoped().Where("1 = 1").Delete(&db.VersionCompareIncludedPackage{})
		}
		// Delete conditions, ops, DRs for tracked delivery requests
		if len(tc.drIDs) > 0 {
			testDB.Unscoped().Where("delivery_request_id IN ?", tc.drIDs).Delete(&db.Condition{})
			testDB.Unscoped().Where("delivery_request_id IN ?", tc.drIDs).Delete(&db.ArtifactTenantOperation{})
			testDB.Unscoped().Where("id IN ?", tc.drIDs).Delete(&db.DeliveryRequest{})
		}
		// Delete artifacts
		if len(tc.artifactIDs) > 0 {
			testDB.Unscoped().Where("id IN ?", tc.artifactIDs).Delete(&db.Artifact{})
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

func (tc *testCleanup) trackTenant(id uint)   { tc.tenantIDs = append(tc.tenantIDs, id) }
func (tc *testCleanup) trackRule(id uint)     { tc.ruleIDs = append(tc.ruleIDs, id) }
func (tc *testCleanup) trackDR(id uint)       { tc.drIDs = append(tc.drIDs, id) }
func (tc *testCleanup) trackArtifact(id uint) { tc.artifactIDs = append(tc.artifactIDs, id) }

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

// mockCPIClientWithDesignTime extends the mock to return specific design-time versions.
// Used by tests that exercise checkVersionDowngradeInTenant.
type mockCPIClientWithDesignTime struct {
	mockCPIClient
	iflowVersions            map[string]string // artifactID → version
	scriptCollectionVersions map[string]string
}

func (m *mockCPIClientWithDesignTime) GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (cpi.IflowItem, error) {
	if v, ok := m.iflowVersions[iflowID]; ok {
		return cpi.IflowItem{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: iflowID, Version: v}}, nil
	}
	return cpi.IflowItem{}, fmt.Errorf("iflow %s not found", iflowID)
}

func (m *mockCPIClientWithDesignTime) GetDesignTimeScriptCollection(ctx context.Context, scID string, scVersion string) (cpi.ScriptCollectionItem, error) {
	if v, ok := m.scriptCollectionVersions[scID]; ok {
		return cpi.ScriptCollectionItem{ArtifactCommonItem: cpi.ArtifactCommonItem{ID: scID, Version: v}}, nil
	}
	return cpi.ScriptCollectionItem{}, fmt.Errorf("script collection %s not found", scID)
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
		// CfApiEndpoint and CfOrg must be unique across active tenants (B1 fix).
		// Use the tenant name as a stable, distinct placeholder.
		CfApiEndpoint: "https://api.test/" + name,
		CfOrg:         "org-" + name,
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

// --- Mock TMS Client ---

// mockTMSClient implements TransportService for testing.
type mockTMSClient struct {
	nodes    []db.TransportNode
	nodesErr error

	routes    []db.TransportRoute
	routesErr error

	importTRResult uint
	importTRErr    error

	// getTransportRequest maps TR number → response. If trErr is set for a number, that error is returned.
	transportRequests map[string]*tms.TransportRequestV1
	trErr             map[string]error

	// trNodeStatuses maps TR number → node status map
	nodeStatuses    map[string]map[uint]tms.TrNodeStatus
	nodeStatusesErr map[string]error
}

func (m *mockTMSClient) GetNodes(ctx context.Context) ([]db.TransportNode, error) {
	return m.nodes, m.nodesErr
}
func (m *mockTMSClient) GetRoutes(ctx context.Context) ([]db.TransportRoute, error) {
	return m.routes, m.routesErr
}
func (m *mockTMSClient) ImportTransportRequest(ctx context.Context, nodeID uint, trs []uint) (uint, error) {
	return m.importTRResult, m.importTRErr
}
func (m *mockTMSClient) GetTransportRequest(ctx context.Context, TrNumber string) (*tms.TransportRequestV1, error) {
	if m.trErr != nil {
		if e, ok := m.trErr[TrNumber]; ok {
			return nil, e
		}
	}
	if m.transportRequests != nil {
		if tr, ok := m.transportRequests[TrNumber]; ok {
			return tr, nil
		}
	}
	return nil, fmt.Errorf("transport request %s not found", TrNumber)
}
func (m *mockTMSClient) TrNodeStatuses(ctx context.Context, trNumber string) (map[uint]tms.TrNodeStatus, error) {
	if m.nodeStatusesErr != nil {
		if e, ok := m.nodeStatusesErr[trNumber]; ok {
			return nil, e
		}
	}
	if m.nodeStatuses != nil {
		if s, ok := m.nodeStatuses[trNumber]; ok {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no node statuses for TR %s", trNumber)
}
func (m *mockTMSClient) ErrLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) ([]string, error) {
	return nil, nil
}
func (m *mockTMSClient) WarnLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) ([]string, error) {
	return nil, nil
}
func (m *mockTMSClient) GetNodeTransportRequests(ctx context.Context, nodeID uint) ([]tms.NodeTransportRequest, error) {
	return nil, nil
}

// --- Mock Notifier ---

// mockNotifier implements Notifier as a no-op for testing.
type mockNotifier struct{}

func (m *mockNotifier) SendApprovalRequest(to []string, drID uint, requestor string, description string) error {
	return nil
}
func (m *mockNotifier) SendDeliveryNotification(to []string, drID uint, status string, message string) error {
	return nil
}
func (m *mockNotifier) AddDeliveryComment(issueKey string, drID uint, message string, status string) error {
	return nil
}

// --- Mock CAS Client ---

// mockCasClient implements CasService for testing.
type mockCasClient struct {
	catalog     []cas.CatalogContentResource
	catalogErr  error
	exportResp  *cas.ExportResponse
	exportErr   error
	pollStatus  *cas.OperationStatus
	pollErr     error
	opConfig    *cas.OperationConfig
	opConfigErr error
}

func (m *mockCasClient) ListContentResources(_ context.Context) ([]cas.CatalogContentResource, error) {
	return m.catalog, m.catalogErr
}
func (m *mockCasClient) TriggerExport(_ context.Context, _ cas.ExportRequest) (*cas.ExportResponse, error) {
	return m.exportResp, m.exportErr
}
func (m *mockCasClient) PollOperation(_ context.Context, _ string) (*cas.OperationStatus, error) {
	return m.pollStatus, m.pollErr
}
func (m *mockCasClient) GetOperationConfig(_ context.Context, _ string) (*cas.OperationConfig, error) {
	return m.opConfig, m.opConfigErr
}

// --- Additional Seed Helpers ---

// seedDeliveryRequest creates a DR in the test DB, tracks it for cleanup, and returns it.
func seedDeliveryRequest(t *testing.T, tc *testCleanup, dr db.DeliveryRequest) db.DeliveryRequest {
	t.Helper()
	if err := testDB.Create(&dr).Error; err != nil {
		t.Fatalf("seedDeliveryRequest failed: %v", err)
	}
	tc.trackDR(dr.ID)
	return dr
}

// seedOp creates an ArtifactTenantOperation in the test DB and returns it.
// The op's DeliveryRequestID should already be tracked by tc.
func seedOp(t *testing.T, op db.ArtifactTenantOperation) db.ArtifactTenantOperation {
	t.Helper()
	if err := testDB.Create(&op).Error; err != nil {
		t.Fatalf("seedOp failed: %v", err)
	}
	return op
}

// seedArtifact creates an Artifact in the test DB, tracks it for cleanup, and returns it.
func seedArtifact(t *testing.T, tc *testCleanup, art db.Artifact) db.Artifact {
	t.Helper()
	if err := testDB.Create(&art).Error; err != nil {
		t.Fatalf("seedArtifact failed: %v", err)
	}
	tc.trackArtifact(art.ID)
	return art
}

// validTR returns a valid RELEASED TransportRequestV1 for testing.
// It matches the given artifact metadata and originates from the given sourceTenantNodeName.
func validTR(trNumber string, sourceTenantNodeName string, artifactTechID string, artifactVersion string, artifactType consts.ArtifactType) *tms.TransportRequestV1 {
	return &tms.TransportRequestV1{
		ID:     1,
		State:  "RELEASED",
		Origin: sourceTenantNodeName,
		Content: []tms.Content{
			{
				Metadata: []tms.Metadata{
					{
						Name:    artifactTechID,
						Version: artifactVersion,
						Type:    artifactType,
					},
				},
			},
		},
	}
}
