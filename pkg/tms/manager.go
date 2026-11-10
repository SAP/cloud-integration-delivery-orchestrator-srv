package tms

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"

	"gorm.io/gorm"
)

// TransportService defines the TMS API operations used by the service layer.
// Satisfied by *TmsClient in production; test code may use a mock.
type TransportService interface {
	GetNodes(ctx context.Context) ([]db.TransportNode, error)
	GetRoutes(ctx context.Context) ([]db.TransportRoute, error)
	ImportTransportRequest(ctx context.Context, nodeID uint, trs []uint) (uint, error)
	GetTransportRequest(ctx context.Context, TrNumber string) (*TransportRequestV1, error)
	TrNodeStatuses(ctx context.Context, trNumber string) (map[uint]TrNodeStatus, error)
	ErrLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) ([]string, error)
	WarnLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) ([]string, error)
	GetNodeTransportRequests(ctx context.Context, nodeID uint) ([]NodeTransportRequest, error)
}

// RuntimeInfo holds metadata about the currently loaded TMS configuration.
type RuntimeInfo struct {
	Loaded          bool       `json:"Loaded"`
	DestinationName string     `json:"TmsApiDestinationName"`
	Endpoint        string     `json:"TmsApiEndpoint,omitempty"`
	LastValidatedAt *time.Time `json:"LastValidatedAt"`
}

// Manager provides thread-safe, cached access to the Central TMS client and
// the destination snapshot used to build it.
//
// The cached entry is the runtime source of truth for Central TMS — not the
// DB row and not the BTP destination. It is written only by the first
// successful lazy load after process start (ensureLoaded) and by a successful
// Reconfigure; every other call reads the cache and never re-resolves the
// destination. There is deliberately no Invalidate.
//
// Why: switching Central TMS also invalidates every tenant's node selection
// and subscriber-side copy (Rule 4), so a change may only take effect through
// Reconfigure, which runs Rule 4 in the same commit. Re-reading the destination
// would let an out-of-band BTP edit silently switch TMS without Rule 4, and
// would let a Destination Service outage break a client that already works.
//
// The Manager does NOT own the configuration version. The authoritative version
// is CentralTmsContext.Generation in the database; Reconfigure's commit callback
// advances it transactionally and SelectTmsNode compares it transactionally. The
// Manager only caches the active client/dest and swaps them atomically (under mu)
// after a successful commit.
type Manager struct {
	mu       sync.Mutex
	database *gorm.DB
	resolver *cf.DestinationServiceClient

	client          TransportService
	dest            *cf.Destination
	destName        string
	lastValidatedAt *time.Time
}

// NewManager creates a production Manager that lazily builds a TMS client
// from the CentralTmsContext in the DB and the provider Destination Service.
func NewManager(database *gorm.DB, resolver *cf.DestinationServiceClient) *Manager {
	return &Manager{database: database, resolver: resolver}
}

// ensureLoaded builds the TMS client on first call. Caller must hold mu.
func (m *Manager) ensureLoaded(ctx context.Context) error {
	if m.client != nil {
		return nil
	}
	if m.database == nil {
		return fmt.Errorf("TMS not configured")
	}

	var tmsCtx db.CentralTmsContext
	if err := m.database.First(&tmsCtx).Error; err != nil {
		return fmt.Errorf("central TMS context not configured: %w", err)
	}
	if m.resolver == nil {
		return fmt.Errorf("destination resolver not injected")
	}
	dest, err := m.resolver.GetDestination(ctx, tmsCtx.TmsApiDestinationName)
	if err != nil {
		return fmt.Errorf("get TMS API destination %q: %w", tmsCtx.TmsApiDestinationName, err)
	}
	if dest == nil {
		return fmt.Errorf("TMS API destination %q not found", tmsCtx.TmsApiDestinationName)
	}
	if dest.ClientId == "" || dest.ClientSecret == "" || dest.TokenServiceURL == "" {
		return fmt.Errorf("TMS API destination %q missing OAuth credentials", tmsCtx.TmsApiDestinationName)
	}

	client, err := NewTmsClient(ctx, dest.URL, dest.TokenServiceURL, dest.ClientId, dest.ClientSecret)
	if err != nil {
		return fmt.Errorf("build TMS client: %w", err)
	}

	m.client = client
	m.dest = dest
	m.destName = tmsCtx.TmsApiDestinationName
	m.lastValidatedAt = tmsCtx.LastValidatedAt
	return nil
}

// Get returns the cached TMS client, building one on first call from DB + resolver.
// Concurrent callers share the same instance. Build failures are not cached.
func (m *Manager) Get(ctx context.Context) (TransportService, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	return m.client, nil
}

// Destination returns a copy of the destination snapshot used to build the
// current client, loading on first call if needed.
func (m *Manager) Destination(ctx context.Context) (cf.Destination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLoaded(ctx); err != nil {
		return cf.Destination{}, err
	}
	return *m.dest, nil
}

// RuntimeInfo returns metadata about the currently loaded configuration,
// loading on first call if needed.
func (m *Manager) RuntimeInfo(ctx context.Context) (RuntimeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureLoaded(ctx); err != nil {
		return RuntimeInfo{}, err
	}
	return RuntimeInfo{
		Loaded:          true,
		DestinationName: m.destName,
		Endpoint:        m.dest.URL,
		LastValidatedAt: m.lastValidatedAt,
	}, nil
}

// Reconfigure is the activation half of a Central TMS configuration change.
// It force-reads the destination from BTP, builds a candidate client, and
// validates it by calling GetNodes — all without holding the lock, so the old
// active client stays usable throughout (§6.3.1).
//
// commit is the caller's DB transaction (version compare-and-set + Rule 4 +
// save). It runs under the Manager lock and receives the validated destination
// snapshot plus the validation timestamp, which it must persist to
// CentralTmsContext.LastValidatedAt in the same transaction so the DB and the
// Manager's in-memory snapshot agree. The optimistic version check lives in
// commit, against the authoritative CentralTmsContext.Generation row (§6.3.2);
// the Manager itself holds no version.
//
// On commit success the active entry (client + dest) is replaced atomically
// under mu, so no in-process reader observes "new DB config + old client"
// (§6.3.3). On any failure — validation or commit (incl. version conflict) —
// the old active entry and DB config are preserved.
func (m *Manager) Reconfigure(ctx context.Context, destName string, commit func(validatedDest cf.Destination, validatedAt time.Time) error) (RuntimeInfo, error) {
	if m.resolver == nil {
		return RuntimeInfo{}, fmt.Errorf("destination resolver not injected")
	}

	// ── Prepare (lock-free): force-read, build, validate ──────────────────
	// Invalidate + GetDestination force-reads the latest destination. The
	// resolver's per-name version guard (stage 2b, §5.3) prevents a concurrent
	// in-flight read from refilling the cache with the stale snapshot.
	m.resolver.Invalidate(destName)
	dest, err := m.resolver.GetDestination(ctx, destName)
	if err != nil {
		return RuntimeInfo{}, fmt.Errorf("force-read destination %q: %w", destName, err)
	}
	if dest == nil {
		return RuntimeInfo{}, fmt.Errorf("destination %q not found", destName)
	}
	if dest.ClientId == "" || dest.ClientSecret == "" || dest.TokenServiceURL == "" {
		return RuntimeInfo{}, fmt.Errorf("destination %q missing OAuth credentials", destName)
	}

	client, err := NewTmsClient(ctx, dest.URL, dest.TokenServiceURL, dest.ClientId, dest.ClientSecret)
	if err != nil {
		return RuntimeInfo{}, fmt.Errorf("build candidate TMS client: %w", err)
	}

	// Validate: authenticate and call GetNodes to verify the TMS system is reachable.
	if _, err := client.GetNodes(ctx); err != nil {
		return RuntimeInfo{}, fmt.Errorf("candidate validation (GetNodes) failed: %w", err)
	}

	// ── Commit (locked): run the DB transaction, then swap the active entry ──
	// now is generated before commit so the DB row and the in-memory snapshot
	// record the same validation timestamp (they must not diverge across restart).
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := commit(*dest, now); err != nil {
		return RuntimeInfo{}, err
	}

	m.client = client
	m.dest = dest
	m.destName = destName
	m.lastValidatedAt = &now

	return RuntimeInfo{
		Loaded:          true,
		DestinationName: m.destName,
		Endpoint:        m.dest.URL,
		LastValidatedAt: m.lastValidatedAt,
	}, nil
}
