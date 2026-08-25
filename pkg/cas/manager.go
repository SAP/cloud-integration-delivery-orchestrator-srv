package cas

import (
	"context"
	"fmt"
	"sync"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cf"

	"gorm.io/gorm"
)

// Manager provides thread-safe, cached access to CAS clients.
// Clients are created lazily on first access and reused thereafter.
// Token refresh is handled by the underlying HttpClient's 401 retry mechanism.
type Manager struct {
	mu       sync.RWMutex
	clients  map[uint]*CasClient
	database *gorm.DB
	resolver *cf.DestinationServiceClient
}

func NewManager(database *gorm.DB, resolver *cf.DestinationServiceClient) *Manager {
	return &Manager{clients: make(map[uint]*CasClient), database: database, resolver: resolver}
}

// Get returns a cached CAS client for the given tenant, creating one if needed.
// Thread-safe via double-checked locking.
func (m *Manager) Get(ctx context.Context, tenantID uint) (*CasClient, error) {
	// Fast path: read lock
	m.mu.RLock()
	if cli, ok := m.clients[tenantID]; ok {
		m.mu.RUnlock()
		return cli, nil
	}
	m.mu.RUnlock()

	// Slow path: write lock, double-check
	m.mu.Lock()
	defer m.mu.Unlock()
	if cli, ok := m.clients[tenantID]; ok {
		return cli, nil
	}
	cli, err := m.newClient(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	m.clients[tenantID] = cli
	return cli, nil
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
