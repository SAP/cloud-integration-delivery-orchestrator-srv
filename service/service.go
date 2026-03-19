package service

import (
	"context"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/notify"
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
}

// IntegrationService defines the CPI operations needed by the service layer.
// It is a facade over CpiClient for testability — all methods are direct pass-throughs.
type IntegrationService interface {
	DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error)
	RuntimeArtifact(ctx context.Context, artifactId string) (cpi.RuntimeArtifact, error)
	GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (cpi.IflowItem, error)
	GetDesignTimeScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) (cpi.ScriptCollectionItem, error)

	// Version Compare: batch query capabilities
	GetPackages(ctx context.Context) ([]cpi.CPIPackage, error)
	GetPackageIflows(ctx context.Context, packageID string) ([]cpi.IflowItem, error)
	GetPackageScriptcollections(ctx context.Context, packageID string) ([]cpi.ScriptCollectionItem, error)
	GetRuntimeArtifacts(ctx context.Context) ([]cpi.RuntimeArtifact, error)
}

// IntegrationFactory creates or retrieves a cached CPI client for a given tenant.
type IntegrationFactory func(ctx context.Context, tenant string) (IntegrationService, error)

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
	TMS          TransportService
	CPI          IntegrationFactory
	GetUserEmail func(ctx context.Context, userID string) (string, error)
	Notifier     Notifier
	EventBus     *EventBus
}

// --- Default Notifier implementation (wraps pkg/notify package functions) ---

type defaultNotifier struct{}

func NewDefaultNotifier() Notifier {
	return &defaultNotifier{}
}

func (n *defaultNotifier) SendApprovalRequest(to []string, drID uint, requestor string, description string) error {
	return notify.SendApprovalRequest(to, drID, requestor, description)
}

func (n *defaultNotifier) SendDeliveryNotification(to []string, drID uint, status string, message string) error {
	return notify.SendDeliveryNotification(to, drID, status, message)
}

func (n *defaultNotifier) AddDeliveryComment(issueKey string, drID uint, message string, status string) error {
	return notify.AddDeliveryComment(issueKey, drID, message, status)
}
