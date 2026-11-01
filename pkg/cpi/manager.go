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
type Manager struct {
	mu       sync.RWMutex
	clients  map[uint]*CpiClient
	database *gorm.DB
	resolver *cf.DestinationServiceClient
}

func NewManager(database *gorm.DB, resolver *cf.DestinationServiceClient) *Manager {
	return &Manager{
		clients:  make(map[uint]*CpiClient),
		database: database,
		resolver: resolver,
	}
}

// Get returns a cached CPI client for the given tenant, creating one if needed.
// Thread-safe via double-checked locking.
func (m *Manager) Get(ctx context.Context, tenantID uint) (IntegrationService, error) {
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

	// Look up tenant's current destination binding.
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
	m.clients[tenantID] = cli
	return cli, nil
}
