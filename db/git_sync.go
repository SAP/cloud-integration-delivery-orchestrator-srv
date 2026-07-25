package db

import (
	"time"

	"mmt-delivery/consts"

	"gorm.io/gorm"
)

// GitRepoConfig stores the Git repository business configuration for artifact code sync.
// Auth credentials are resolved via BTP Destination Service using DestinationName.
type GitRepoConfig struct {
	gorm.Model
	Provider        string `gorm:"not null" json:"provider"`        // github | github_enterprise
	DestinationName string `gorm:"not null" json:"destinationName"` // BTP Destination → resolves to API URL + credentials
	Owner           string `gorm:"not null" json:"owner"`           // GitHub org or user
	Repo            string `gorm:"not null" json:"repo"`            // repository name
	Enabled         bool   `gorm:"default:false" json:"enabled"`
}

// GitArtifactSnapshot records a single sync event — one artifact version pushed to GitHub.
// It serves as the authoritative pointer for code compare (CommitSHA + TreePath).
type GitArtifactSnapshot struct {
	gorm.Model
	ArtifactID   string              `gorm:"not null;index:idx_git_snapshot_artifact_version,unique" json:"artifactId"`
	Version      string              `gorm:"not null;index:idx_git_snapshot_artifact_version,unique" json:"version"`
	CpiTenantID  uint                `gorm:"not null;index:idx_git_snapshot_artifact_version,unique" json:"cpiTenantId"`
	PackageID    string              `gorm:"not null" json:"packageId"`
	ArtifactType consts.ArtifactType `gorm:"not null" json:"artifactType"`

	// Git references
	BranchName string `gorm:"not null" json:"branchName"` // tenant/cpi-dev
	TreePath   string `gorm:"not null" json:"treePath"`   // packages/<pkg>/<artifact>
	CommitSHA  string `json:"commitSHA"`                  // 40-char hex, populated on completion
	TagName    string `json:"tagName"`                    // tenant/<tenant>/<pkg>/<artifact>/<version>

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
