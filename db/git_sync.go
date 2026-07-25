package db

import (
	"time"

	"gorm.io/gorm"
)

// GitRepositoryConfig stores the GitHub / GitHub Enterprise repository configuration
// for artifact code sync. At most one active config per deployment.
type GitRepositoryConfig struct {
	gorm.Model
	Provider   string `gorm:"not null" json:"provider"`   // GITHUB | GITHUB_ENTERPRISE
	BaseURL    string `gorm:"not null" json:"baseURL"`    // repo web URL (e.g. https://github.com/org/repo)
	APIBaseURL string `gorm:"not null" json:"apiBaseURL"` // API endpoint (e.g. https://api.github.com)
	Owner      string `gorm:"not null" json:"owner"`
	Repo       string `gorm:"not null" json:"repo"`
	AuthToken  string `gorm:"not null" json:"-"` // service principal token, never exposed in JSON
	Enabled    bool   `gorm:"default:false" json:"enabled"`
}

// GitArtifactSnapshot records a single sync event — one artifact version pushed to GitHub.
// It serves as the authoritative pointer for code compare (CommitSHA + TreePath).
type GitArtifactSnapshot struct {
	gorm.Model
	ArtifactID   string `gorm:"not null;index:idx_git_snapshot_artifact_version,unique" json:"artifactId"`
	Version      string `gorm:"not null;index:idx_git_snapshot_artifact_version,unique" json:"version"`
	CpiTenantID  uint   `gorm:"not null;index:idx_git_snapshot_artifact_version,unique" json:"cpiTenantId"`
	PackageID    string `gorm:"not null" json:"packageId"`
	ArtifactType string `gorm:"not null" json:"artifactType"` // iflow | script_collection

	// Git references
	BranchName string `gorm:"not null" json:"branchName"` // tenant/cpi-dev
	TreePath   string `gorm:"not null" json:"treePath"`   // packages/<pkg>/<artifact>
	CommitSHA  string `json:"commitSHA"`                  // 40-char hex, populated on completion
	TagName    string `json:"tagName"`                    // tenant/<tenant>/<pkg>/<artifact>/<version>

	// Observation context (from CPI API at sync time)
	ObservedModifiedBy string `json:"observedModifiedBy,omitempty"`
	ObservedModifiedAt string `json:"observedModifiedAt,omitempty"`

	// Trigger context
	TriggerSource     string `gorm:"not null" json:"triggerSource"` // DR | CRON | IMPORT | MANUAL
	DeliveryRequestID *uint  `json:"deliveryRequestId,omitempty"`
	ArtifactOpID      *uint  `json:"artifactOpId,omitempty"`

	// Lifecycle
	Status      string     `gorm:"not null;default:'pending'" json:"status"` // pending | completed | failed
	TriggeredAt time.Time  `gorm:"not null" json:"triggeredAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
}
