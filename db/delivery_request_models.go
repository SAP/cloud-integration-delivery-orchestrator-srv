package db

import (
	"mmt-delivery/pkg/lifecycle"
	"time"

	"gorm.io/gorm"
)

type DeliveryRequest struct {
	gorm.Model
	Name string

	JiraLink        string                    // related Jira ticket URL
	AggregateStatus lifecycle.AggregateStatus // pending, in-progress, completed, failed

	ApprovedBy string // user who approved the import
	ApprovedAt *time.Time

	// One-to-many: a delivery request has many artifacts
	ArtifactTenantOperations []ArtifactTenantOperation `gorm:"foreignKey:DeliveryRequestID"`

	SourceTenantID *uint
	SourceTenant   *CpiTenant `gorm:"foreignKey:SourceTenantID"`

	DeliveryRuleID *uint
	DeliveryRule   *DeliveryRule `gorm:"foreignKey:DeliveryRuleID"`

	TargetNodes  []TransportNode  `gorm:"serializer:json"`
	TargetRoutes []TransportRoute `gorm:"serializer:json"`

	CreatedBy string
	UpdatedBy string
}

// ArtifactTenantOperation represents the lifecycle of a single artifact for a single CPI tenant.
// It captures per-phase states (transport/import/deploy), retry metadata, and conditions.
// One row per (artifact_id, tenant_id) pair.
type ArtifactTenantOperation struct {
	gorm.Model
	DeliveryRequestID uint `gorm:"index;not null"`

	// for faster query and write, we use artifact_id as foreign key
	// no need to CASCADE, artifact:version is like a metadata
	ArtifactID uint     `gorm:"index;not null"`
	Artifact   Artifact `gorm:"foreignKey:ArtifactID;references:ID"`

	ArtifactTechID  string
	ArtifactVersion string

	TenantID uint
	Tenant   CpiTenant `gorm:"foreignKey:TenantID"`

	TransportRequestNumber string // associated transport request number

	// All 3 Phases
	RequestState lifecycle.RequestState
	ImportState  lifecycle.ImportState
	DeployState  lifecycle.DeployState

	LastError        string
	RetryCountImport int
	RetryCountDeploy int
	NextRetryAt      *time.Time
	Conditions       []byte // TODO: may asscociate with lifecycle.Condition. 'Actions' may be better name(ask AI to confirm)

	CreatedBy string
	UpdatedBy string
}

// BatchJob orchestrates a bulk set of operations (IMPORT or DEPLOY) across a collection
// of artifacts and/or tenants. It aggregates progress and exposes a summarized status.
type BatchJob struct {
	gorm.Model

	JobType   string // IMPORT | DEPLOY
	ScopeType string // BY_TENANT | BY_ARTIFACT

	TenantID   *uint // non-nil when scope is BY_TENANT
	ArtifactID *uint // non-nil when scope is BY_ARTIFACT

	AggregateStatus lifecycle.AggregateStatus // may have partial status

	SuccessCount int
	FailedCount  int
	TotalItems   int

	Conditions []byte // JSON encoded []status.Condition
	CreatedBy  string
}

// bind cpi tenant with tms node
type CpiTenant struct {
	gorm.Model
	Name                     string `gorm:"uniqueIndex,where:deleted_at IS NULL"` // grom tag for soft delete issue. cpi-mmt-dev, cpi-ci, may use cpi tenant domain
	CreatedBy                string
	UpdatedBy                string
	TransportNodeID          uint        //TMS Node ID
	TransportNodeName        string      // TMS Node Name, for easier query
	TransportNodeDescription string      // TMS Node Description
	CpiEndpoint              ApiEndpoint `gorm:"serializer:json"`
}

type DeliveryRule struct {
	gorm.Model
	Name           string
	VersionPattern string

	// Associations to CpiTenant
	IncludedTenants []CpiTenant `gorm:"serializer:json;"` // included CPI tenants
	ExcludedTenants []CpiTenant `gorm:"serializer:json;"` // excluded CPI tenants

	Active    bool
	CreatedBy string
	UpdatedBy string
}
