package service

import (
	"context"
	"sync"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cas"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/notify"
	cpiotel "mmt-delivery/pkg/otel"
	"mmt-delivery/pkg/tms"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// --- Interfaces for external dependencies (used by Service for testability) ---

// TransportService defines the TMS operations needed by the service layer.
type TransportService interface {
	GetNodes(ctx context.Context) ([]db.TransportNode, error)
	GetRoutes(ctx context.Context) ([]db.TransportRoute, error)
	ImportTransportRequest(ctx context.Context, nodeID uint, trs []uint) (uint, error)
	GetTransportRequest(ctx context.Context, TrNumber string) (*tms.TransportRequestV1, error)
	TrNodeStatuses(ctx context.Context, trNumber string) (map[uint]tms.TrNodeStatus, error)
	ErrLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) ([]string, error)
	WarnLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) ([]string, error)
	GetNodeTransportRequests(ctx context.Context, nodeID uint) ([]tms.NodeTransportRequest, error)
}

// IntegrationService defines the CPI operations needed by the service layer.
// It is a facade over CpiClient for testability — all methods are direct pass-throughs.
//
// Two query patterns exist for design-time artifacts:
//
//   - Package Artifacts (GetPackageArtifactsByType): queries via Navigation Property.
//     Draft artifacts return Version="Active" (not the real version number).
//     Used for discovery/listing.
//
//   - Direct Artifact (GetDesignTimeArtifact): queries by artifact ID + version.
//     When version="active" is passed, returns the actual formal version (e.g. "6.2.9").
//     Used for version downgrade checks where the real version is needed.
type IntegrationService interface {
	DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error)
	RuntimeArtifact(ctx context.Context, artifactId string) (cpi.RuntimeArtifact, error)
	GetDesignTimeArtifact(ctx context.Context, artifactID, version string, artifactType consts.ArtifactType) (cpi.ArtifactCommonItem, error)
	GetPackages(ctx context.Context) ([]cpi.CPIPackage, error)
	GetPackageArtifactsByType(ctx context.Context, packageID string, artifactType consts.ArtifactType) ([]cpi.ArtifactCommonItem, error)
	GetRuntimeArtifacts(ctx context.Context) ([]cpi.RuntimeArtifact, error)

	// Git Sync: download artifact ZIP content
	DownloadArtifactZip(ctx context.Context, artifactID, version string, artifactType consts.ArtifactType) ([]byte, error)
}

// IntegrationFactory creates or retrieves a cached CPI client for a given tenant.
type IntegrationFactory func(ctx context.Context, tenant string) (IntegrationService, error)

// CasService defines the CAS operations needed by the service layer.
type CasService interface {
	ListCloudIntegrationResources(ctx context.Context, packageIDs []string) ([]cas.CatalogContentResource, error)
	TriggerExport(ctx context.Context, req cas.ExportRequest) (*cas.ExportResponse, error)
	PollOperation(ctx context.Context, processID string) (*cas.OperationStatus, error)
	GetOperationConfig(ctx context.Context, processID string) (*cas.OperationConfig, error)
}

// CasFactory resolves and returns a ready-to-use CasService for a given tenant.
// Production value built by NewCasFactory; test value is a literal returning a mock.
type CasFactory func(ctx context.Context, tenantID uint) (CasService, error)

// Notifier abstracts notification operations (email, JIRA).
type Notifier interface {
	SendApprovalRequest(to []string, drID uint, requestor string, description string) error
	SendDeliveryNotification(to []string, drID uint, status string, message string) error
	AddDeliveryComment(issueKey string, drID uint, message string, status string) error
}

// --- Service struct ---

// Service holds all injected dependencies for the business logic layer.
// All service-layer functions are methods on this struct.
type Service struct {
	DB           *gorm.DB
	Logger       *zap.SugaredLogger
	TmsSvc       TmsClientFunc
	CPI          IntegrationFactory
	CAS          CasFactory
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

// --- Default Notifier implementation (wraps pkg/notify package functions) ---

type defaultNotifier struct {
	resolver *cf.DestinationServiceClient
	database *gorm.DB
}

func NewDefaultNotifier(resolver *cf.DestinationServiceClient, database *gorm.DB) Notifier {
	return &defaultNotifier{resolver: resolver, database: database}
}

func (n *defaultNotifier) smtpDest() string {
	var cfg db.IntegrationConfig
	if err := n.database.Where("type = ?", "smtp").First(&cfg).Error; err != nil || !cfg.Enabled {
		return ""
	}
	return cfg.DestinationName
}

func (n *defaultNotifier) jiraDest() string {
	var cfg db.IntegrationConfig
	if err := n.database.Where("type = ?", "jira").First(&cfg).Error; err != nil || !cfg.Enabled {
		return ""
	}
	return cfg.DestinationName
}

func (n *defaultNotifier) SendApprovalRequest(to []string, drID uint, requestor string, description string) error {
	dest := n.smtpDest()
	if dest == "" {
		return nil // SMTP not configured, silently skip
	}
	return notify.SendApprovalRequest(n.resolver, dest, to, drID, requestor, description)
}

func (n *defaultNotifier) SendDeliveryNotification(to []string, drID uint, status string, message string) error {
	dest := n.smtpDest()
	if dest == "" {
		return nil
	}
	return notify.SendDeliveryNotification(n.resolver, dest, to, drID, status, message)
}

func (n *defaultNotifier) AddDeliveryComment(issueKey string, drID uint, message string, status string) error {
	dest := n.jiraDest()
	if dest == "" {
		return nil // JIRA not configured, silently skip
	}
	return notify.AddDeliveryComment(n.resolver, dest, issueKey, drID, message, status)
}
