package cpi

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/consts"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"

	"gorm.io/gorm"
)

// IntegrationService defines the CPI operations used by the service layer.
// Satisfied by *CpiClient in production; test code may use a mock.
type IntegrationService interface {
	DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error)
	RuntimeArtifact(ctx context.Context, artifactId string) (RuntimeArtifact, error)
	GetDesignTimeArtifact(ctx context.Context, artifactID, version string, artifactType consts.ArtifactType) (ArtifactCommonItem, error)
	GetPackages(ctx context.Context) ([]CPIPackage, error)
	GetPackageArtifactsByType(ctx context.Context, packageID string, artifactType consts.ArtifactType) ([]ArtifactCommonItem, error)
	GetRuntimeArtifacts(ctx context.Context) ([]RuntimeArtifact, error)
	DownloadArtifactZip(ctx context.Context, artifactID, version string, artifactType consts.ArtifactType) ([]byte, error)
}

// ErrNoDestination is returned when a tenant has no PirApiDestinationName configured.
// Callers (e.g. HTTP handlers) can check this to return a 400 "bootstrap required".
var ErrNoDestination = errors.New("tenant has no CPI destination configured")

// Manager provides thread-safe, cached access to CPI clients keyed by tenant ID.
// Clients are created lazily on first access and reused thereafter.
// Token refresh is handled by the underlying HttpClient's 401 retry mechanism.
//
// The lock (mu) guards only the map reads/writes and the per-tenant version
// counter (gen); the slow work — DB lookup, destination resolution, client
// build — runs OFF the lock so one tenant's first Get cannot block another's
// (this manager serves large numbers of tenants). gen lets a concurrent
// Invalidate cancel an in-flight build's write-back, preventing a stale client
// from being published after eviction (§5.3). One lock, no atomics.
type Manager struct {
	mu       sync.RWMutex
	clients  map[uint]*CpiClient
	gen      map[uint]uint64
	database *gorm.DB
	resolver *cf.DestinationServiceClient
}

func NewManager(database *gorm.DB, resolver *cf.DestinationServiceClient) *Manager {
	return &Manager{
		clients:  make(map[uint]*CpiClient),
		gen:      make(map[uint]uint64),
		database: database,
		resolver: resolver,
	}
}

// Get returns a cached CPI client for the given tenant, creating one if needed.
// The build runs without holding the lock; a per-tenant version guards the
// write-back so a concurrent Invalidate is never overwritten by a stale build.
func (m *Manager) Get(ctx context.Context, tenantID uint) (IntegrationService, error) {
	// Fast path: cached client.
	m.mu.RLock()
	if cli, ok := m.clients[tenantID]; ok {
		m.mu.RUnlock()
		return cli, nil
	}
	genStart := m.gen[tenantID]
	m.mu.RUnlock()

	// Slow path (off-lock): resolve binding + build client.
	var tenant db.CpiTenant
	if err := m.database.WithContext(ctx).Select("id", "pir_api_destination_name").First(&tenant, tenantID).Error; err != nil {
		return nil, fmt.Errorf("CPI manager: load tenant %d: %w", tenantID, err)
	}
	if tenant.PirApiDestinationName == "" {
		return nil, fmt.Errorf("CPI manager: tenant %d: %w", tenantID, ErrNoDestination)
	}

	dest, err := m.resolver.GetDestination(ctx, tenant.PirApiDestinationName)
	if err != nil {
		return nil, fmt.Errorf("CPI manager: resolve destination %q: %w", tenant.PirApiDestinationName, err)
	}
	if dest == nil {
		return nil, fmt.Errorf("CPI manager: destination %q not found", tenant.PirApiDestinationName)
	}
	if dest.ClientId == "" || dest.ClientSecret == "" || dest.TokenServiceURL == "" {
		return nil, fmt.Errorf("CPI manager: destination %q missing OAuth credentials", tenant.PirApiDestinationName)
	}

	cli, err := NewClient(ctx, *dest)
	if err != nil {
		return nil, fmt.Errorf("CPI manager: build client for tenant %d: %w", tenantID, err)
	}

	// Publish under the lock, but only if no Invalidate advanced the version
	// while we built off-lock. On mismatch, return the freshly-built client to
	// this caller without caching it (next Get rebuilds).
	//
	// De-duplicate concurrent misses: if another goroutine already cached a
	// client for this tenant, discard ours and reuse theirs. This is required
	// (not just tidy) because each client owns a concurrency-limiting semaphore;
	// caching two instances for one tenant would double its effective limit
	// (RFC 033 §5.4). The resolver does NOT need this — it caches immutable
	// Destination snapshots, not stateful clients.
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.clients[tenantID]; ok {
		return existing, nil
	}
	if m.gen[tenantID] == genStart {
		m.clients[tenantID] = cli
	}
	return cli, nil
}

// Invalidate removes the cached CPI client for the given tenant and advances its
// version so an in-flight Get cannot write a stale client back.
// The next Get call will re-read the tenant binding and rebuild the client.
func (m *Manager) Invalidate(tenantID uint) {
	m.mu.Lock()
	delete(m.clients, tenantID)
	m.gen[tenantID]++
	m.mu.Unlock()
}
