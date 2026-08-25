package cpi

import (
	"context"
	"fmt"
	"mmt-delivery/pkg/cf"
	"sync"
)

// Manager provides thread-safe, cached access to CPI clients.
// Clients are created lazily on first access and reused thereafter.
// Token refresh is handled by the underlying HttpClient's 401 retry mechanism.
type Manager struct {
	mu       sync.RWMutex
	clients  map[string]*CpiClient
	resolver *cf.DestinationServiceClient
}

func NewManager(resolver *cf.DestinationServiceClient) *Manager {
	return &Manager{clients: make(map[string]*CpiClient), resolver: resolver}
}

// Get returns a cached CPI client for the given BTP destination name, creating one if needed.
// Thread-safe via double-checked locking.
func (m *Manager) Get(ctx context.Context, destinationName string) (*CpiClient, error) {
	// Fast path: read lock
	m.mu.RLock()
	if cli, ok := m.clients[destinationName]; ok {
		m.mu.RUnlock()
		return cli, nil
	}
	m.mu.RUnlock()

	// Slow path: write lock, double-check
	m.mu.Lock()
	defer m.mu.Unlock()
	if cli, ok := m.clients[destinationName]; ok {
		return cli, nil
	}
	cli, err := NewClient(ctx, destinationName, m.resolver)
	if err != nil {
		return nil, fmt.Errorf("failed to create CPI client for destination %s: %w", destinationName, err)
	}
	m.clients[destinationName] = cli
	return cli, nil
}
