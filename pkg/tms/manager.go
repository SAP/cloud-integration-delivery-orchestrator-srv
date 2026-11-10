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
// The authoritative configuration version is CentralTmsContext.Generation in
// the database; Reconfigure's commit callback advances it transactionally. The
// Manager caches a read-only copy ("source tag") recording which DB version the
// active client was built from, so callers (SelectTmsNode) obtain client and
// generation from one atomic snapshot under mu — business callers never read
// db.CentralTmsContext directly for the active version. Cold-load and the GET
// handler's fallback read the config row for initialisation / display purposes,
// not for runtime version decisions.
//
// Lock-ordering constraint: Reconfigure runs its commit callback (a DB
// transaction) while holding mu. Callers must therefore NOT invoke Get /
// Destination / RuntimeInfo / Reconfigure from inside a DB transaction that also
// contends with that commit — doing so could wait on mu while holding a DB row
// lock the commit needs, a deadlock the database cannot detect.
type Manager struct {
	mu       sync.Mutex
	database *gorm.DB
	resolver *cf.DestinationServiceClient

	client          TransportService
	dest            *cf.Destination
	destName        string
	generation      uint64
	lastValidatedAt *time.Time
}

// NewManager creates a production Manager that lazily builds a TMS client
// from the CentralTmsContext in the DB and the provider Destination Service.
func NewManager(database *gorm.DB, resolver *cf.DestinationServiceClient) *Manager {
	return &Manager{database: database, resolver: resolver}
}

// load builds the TMS client on first use. The slow work — reading the config
// row, resolving the destination, building the client — runs OFF the lock, so a
// slow or unavailable Destination Service does not serialise every TMS caller
// behind one in-flight request (L1). The lock is held only to check/publish the
// cached entry. Concurrent first callers each build, then the first to re-take
// the lock publishes; the rest observe a non-nil client and discard their build.
// Build failures are not cached, so the next call retries.
func (m *Manager) load(ctx context.Context) error {
	m.mu.Lock()
	if m.client != nil {
		m.mu.Unlock()
		return nil
	}
	db0 := m.database
	resolver := m.resolver
	m.mu.Unlock()

	if db0 == nil {
		return fmt.Errorf("TMS not configured")
	}
	if resolver == nil {
		return fmt.Errorf("destination resolver not injected")
	}

	// ── Off-lock: read config + resolve + build ──────────────────────────
	var tmsCtx db.CentralTmsContext
	if err := db0.First(&tmsCtx).Error; err != nil {
		return fmt.Errorf("central TMS context not configured: %w", err)
	}
	dest, err := resolver.GetDestination(ctx, tmsCtx.TmsApiDestinationName)
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

	// ── Publish under the lock; a concurrent builder may have won ─────────
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		return nil
	}
	m.client = client
	m.dest = dest
	m.destName = tmsCtx.TmsApiDestinationName
	m.generation = tmsCtx.Generation
	m.lastValidatedAt = tmsCtx.LastValidatedAt
	return nil
}

// Get returns the cached TMS client together with the DB generation it was
// built from, building one on first call from DB + resolver. Concurrent callers
// share the same instance; build failures are not cached. Client and generation
// are one atomic snapshot taken under mu, so a caller that compares the active
// client against a config version (SelectTmsNode's L20 optimistic check) gets
// both from the same source of truth and they cannot drift apart due to call
// ordering — callers never read db.CentralTmsContext for the active version.
// The generation is the DB value captured at cold-load or last Reconfigure;
// callers that need the version still re-read and compare it under FOR UPDATE
// inside their own transaction (that persistent-layer optimistic lock is
// unchanged). Callers that only need the client discard the generation.
func (m *Manager) Get(ctx context.Context) (TransportService, uint64, error) {
	if err := m.load(ctx); err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client, m.generation, nil
}

// Destination returns a copy of the destination snapshot used to build the
// current client, loading on first call if needed.
func (m *Manager) Destination(ctx context.Context) (cf.Destination, error) {
	if err := m.load(ctx); err != nil {
		return cf.Destination{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.dest, nil
}

// RuntimeInfo returns metadata about the currently loaded configuration,
// loading on first call if needed.
func (m *Manager) RuntimeInfo(ctx context.Context) (RuntimeInfo, error) {
	if err := m.load(ctx); err != nil {
		return RuntimeInfo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
// Manager's in-memory snapshot agree. It returns the new generation it wrote
// (currentGen+1), which the Manager stores as the source tag of the swapped-in
// client. The optimistic version check lives in commit, against the
// authoritative CentralTmsContext.Generation row (§6.3.2).
//
// On commit success the active entry (client + dest + generation) is replaced
// atomically under mu, so no in-process reader observes "new DB config + old
// client" (§6.3.3). On any failure — validation or commit (incl. version
// conflict) — the old active entry and DB config are preserved.
func (m *Manager) Reconfigure(ctx context.Context, destName string, commit func(validatedDest cf.Destination, validatedAt time.Time) (uint64, error)) (RuntimeInfo, error) {
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

	newGen, err := commit(*dest, now)
	if err != nil {
		return RuntimeInfo{}, err
	}

	m.client = client
	m.dest = dest
	m.destName = destName
	m.generation = newGen
	m.lastValidatedAt = &now

	return RuntimeInfo{
		Loaded:          true,
		DestinationName: m.destName,
		Endpoint:        m.dest.URL,
		LastValidatedAt: m.lastValidatedAt,
	}, nil
}
