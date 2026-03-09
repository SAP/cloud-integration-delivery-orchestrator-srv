package cpi

import (
	"context"
	"fmt"
	"sync"
)

// Manager provides thread-safe, cached access to CPI clients.
// Clients are created lazily on first access and reused thereafter.
// Token refresh is handled by the underlying HttpClient's 401 retry mechanism.
type Manager struct {
	mu      sync.RWMutex
	clients map[string]*CpiClient
}

func NewManager() *Manager {
	return &Manager{clients: make(map[string]*CpiClient)}
}

// Get returns a cached CPI client for the given tenant, creating one if needed.
// Thread-safe via double-checked locking.
func (m *Manager) Get(ctx context.Context, tenant string) (*CpiClient, error) {
	// Fast path: read lock
	m.mu.RLock()
	if cli, ok := m.clients[tenant]; ok {
		m.mu.RUnlock()
		return cli, nil
	}
	m.mu.RUnlock()

	// Slow path: write lock, double-check
	m.mu.Lock()
	defer m.mu.Unlock()
	if cli, ok := m.clients[tenant]; ok {
		return cli, nil
	}
	cli, err := NewClient(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("failed to create CPI client for tenant %s: %w", tenant, err)
	}
	m.clients[tenant] = cli
	return cli, nil
}
