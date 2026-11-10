package service

import (
	"context"
	"sync"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cas"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cpi"
	cpiotel "github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/otel"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/tms"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// --- Provider interfaces (consumer-defined, satisfied by *Manager in each package) ---

// TmsProvider abstracts TMS manager capabilities needed by the service layer.
// Production: *tms.Manager. Tests: mock in test files.
type TmsProvider interface {
	Get(ctx context.Context) (tms.TransportService, uint64, error)
	Destination(ctx context.Context) (cf.Destination, error)
	RuntimeInfo(ctx context.Context) (tms.RuntimeInfo, error)
	Reconfigure(ctx context.Context, destName string, commit func(validatedDest cf.Destination, validatedAt time.Time) (uint64, error)) (tms.RuntimeInfo, error)
}

// CpiProvider abstracts CPI manager capabilities needed by the service layer.
// Production: *cpi.Manager. Tests: mock in test files.
type CpiProvider interface {
	Get(ctx context.Context, tenantID uint) (cpi.IntegrationService, error)
	Invalidate(tenantID uint)
}

// CasProvider abstracts CAS manager capabilities needed by the service layer.
// Production: *cas.Manager. Tests: mock in test files.
type CasProvider interface {
	Get(ctx context.Context, tenantID uint) (cas.CasService, error)
	Invalidate(tenantID uint)
}

// Notifier abstracts delivery event notifications.
// Implementations decide the delivery channel (ANS, Jira API, etc.).
type Notifier interface {
	OnApprovalRequested(drID uint, requestor string, description string) error
	OnStatusChanged(drID uint, status string, message string) error
	OnDeliveryComment(issueKey string, drID uint, message string, status string) error
}

// --- Service struct ---

// Service holds all injected dependencies for the business logic layer.
// All service-layer functions are methods on this struct.
type Service struct {
	DB           *gorm.DB
	Logger       *zap.SugaredLogger
	TMS          TmsProvider
	CPI          CpiProvider
	CAS          CasProvider
	GetUserEmail func(ctx context.Context, userID string) (string, error)
	Notifier     Notifier
	Hub         *WSHub       // WebSocket hub for per-DR event push; nil-safe (no-op when nil)
	SyncTracker *SyncTracker // manages per-DR sync goroutines; nil-safe (no-op when nil)
	ProviderDest *cf.DestinationServiceClient // provider-side Destination Service; used by bootstrap to write per-tenant CPIDELIVERY_* destinations

	// drSyncLocks prevents concurrent SyncDeliveryStatus calls for the same DR.
	// Without this guard, two simultaneous sync requests can both read "no op exists"
	// and each INSERT a new ArtifactTenantOperation, producing duplicate rows that
	// then permanently diverge in import state.
	drSyncLocks sync.Map // key: deliveryRequestID (uint), value: struct{}
}

// L returns a child logger enriched with trace_id and span_id from ctx.
// When ctx has no active span (e.g. CLS not enabled), returns s.Logger unchanged.
func (s *Service) L(ctx context.Context) *zap.SugaredLogger {
	return cpiotel.WithTrace(ctx, s.Logger)
}
