package cas

import (
	"context"
	"fmt"
	"sync"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"

	"gorm.io/gorm"
)

// CasService defines the CAS operations used by the service layer.
// Satisfied by *CasClient in production; test code may use a mock.
type CasService interface {
	ListCloudIntegrationResources(ctx context.Context, packageIDs []string) ([]CatalogContentResource, error)
	TriggerExport(ctx context.Context, req ExportRequest) (*ExportResponse, error)
	PollOperation(ctx context.Context, processID string) (*OperationStatus, error)
	GetOperationConfig(ctx context.Context, processID string) (*OperationConfig, error)
}

// Manager provides thread-safe, cached access to CAS clients.
// Clients are created lazily on first access and reused thereafter.
// Token refresh is handled by the underlying HttpClient's 401 retry mechanism.
//
// Like the CPI manager, mu guards only the map and the per-tenant version
// counter (gen); newClient (DB lookup, destination resolution, client build)
// runs OFF the lock so one tenant's first Get cannot block another's. gen lets
// a concurrent Invalidate cancel an in-flight build's write-back (§5.3). One
// lock, no atomics.
type Manager struct {
	mu       sync.RWMutex
	clients  map[uint]*CasClient
	gen      map[uint]uint64
	database *gorm.DB
	resolver *cf.DestinationServiceClient
}

func NewManager(database *gorm.DB, resolver *cf.DestinationServiceClient) *Manager {
	return &Manager{clients: make(map[uint]*CasClient), gen: make(map[uint]uint64), database: database, resolver: resolver}
}

// Get returns a cached CAS client for the given tenant, creating one if needed.
// The build runs without holding the lock; a per-tenant version guards the
// write-back so a concurrent Invalidate is never overwritten by a stale build.
func (m *Manager) Get(ctx context.Context, tenantID uint) (CasService, error) {
	// Fast path: cached client.
	m.mu.RLock()
	if cli, ok := m.clients[tenantID]; ok {
		m.mu.RUnlock()
		return cli, nil
	}
	genStart := m.gen[tenantID]
	m.mu.RUnlock()

	// Slow path (off-lock): build client.
	cli, err := m.newClient(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Publish under the lock only if no Invalidate advanced the version while we
	// built off-lock. On mismatch, hand the fresh client to this caller without
	// caching (next Get rebuilds). Reuse an already-cached client if one appeared:
	// one client per tenant, so two concurrent misses don't create two semaphores
	// (see pkg/cpi/manager.go Get for the full rationale).
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

// Invalidate removes the cached CAS client for the given tenant and advances its
// version so an in-flight Get cannot write a stale client back.
// The next Get call will re-read the tenant binding and rebuild the client.
func (m *Manager) Invalidate(tenantID uint) {
	m.mu.Lock()
	delete(m.clients, tenantID)
	m.gen[tenantID]++
	m.mu.Unlock()
}

func (m *Manager) newClient(ctx context.Context, tenantID uint) (*CasClient, error) {
	if m.resolver == nil {
		return nil, fmt.Errorf("CAS manager: DestinationServiceClient not injected")
	}
	var tenant db.CpiTenant
	if err := m.database.WithContext(ctx).First(&tenant, tenantID).Error; err != nil {
		return nil, fmt.Errorf("CAS manager: load tenant %d: %w", tenantID, err)
	}
	if tenant.CasEngineDestinationName == "" {
		return nil, fmt.Errorf("CAS manager: tenant %d has no CasEngineDestinationName", tenantID)
	}
	dest, err := m.resolver.GetDestination(ctx, tenant.CasEngineDestinationName)
	if err != nil {
		return nil, fmt.Errorf("CAS manager: resolve destination %q: %w", tenant.CasEngineDestinationName, err)
	}
	if dest == nil {
		return nil, fmt.Errorf("CAS manager: destination %q not found", tenant.CasEngineDestinationName)
	}
	if dest.ClientId == "" || dest.ClientSecret == "" || dest.TokenServiceURL == "" {
		return nil, fmt.Errorf("CAS manager: destination %q missing OAuth credentials", tenant.CasEngineDestinationName)
	}
	return NewCasClient(ctx, dest.URL, dest.TokenServiceURL, dest.ClientId, dest.ClientSecret)
}
