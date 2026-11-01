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
// Stage 1: lazy build on first access, reuse thereafter. No Invalidate or Reconfigure.
// Stage 2 will add Reconfigure (candidate validate → atomic publish + Rule 4).
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
