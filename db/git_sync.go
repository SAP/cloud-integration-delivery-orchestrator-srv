package db

import (
	"time"

	"mmt-delivery/consts"

	"gorm.io/gorm"
)

// GitRepoConfig stores the Git repository business configuration for artifact code sync.
// Auth credentials are resolved via BTP Destination Service using DestinationName.
//
// Fields are grouped by WHO owns (authoritatively writes) them, because the row has two authors
// depending on AuthMethod. The backend is the source of truth for this ownership split: in
// github_app mode the client is NOT trusted to supply the connection-identity/credential fields —
// UpsertGitRepoConfig applies only the client-owned target selection (see handler.updateGitAppTarget),
// while the identity/credential fields are authored solely by the manifest/setup callbacks.
// (Field reordering is column-name-preserving for GORM AutoMigrate — no migration impact.)
type GitRepoConfig struct {
	gorm.Model

	// ── Discriminator ──────────────────────────────────────────────────────────
	// Selects how the row is interpreted and which fields below are authoritative.
	// "pat" (default): PAT in the destination Password; the client authors the whole row.
	// "github_app": GitHub App installation token (base64 PEM private key in the destination
	// Password) with the App/Installation IDs below; the callbacks author the identity/credential
	// fields, the client only authors the target selection. See github.AuthMethodPAT / AuthMethodGitHubApp.
	AuthMethod string `gorm:"default:pat" json:"authMethod"`

	// ── Target selection (client-owned in BOTH modes) ─────────────────────────
	// The user picks these via the config UI. In github_app mode UpsertGitRepoConfig applies ONLY
	// these onto an existing row; everything below is left untouched.
	Provider string `gorm:"not null" json:"provider"` // see github.Provider constants
	Repo     string `gorm:"not null" json:"repo"`     // repository name
	Enabled  bool   `gorm:"default:false" json:"enabled"`

	// ── Connection identity (pat: client-owned · github_app: callback-owned) ───
	// pat mode: entered in the form. github_app mode: written by the manifest callback
	// (DestinationName = "github-app-<slug>", Owner = App creator) and treated as backend
	// source of truth — never taken from the client on update.
	DestinationName string `gorm:"not null" json:"destinationName"` // BTP Destination → resolves to API URL + credentials
	Owner           string `gorm:"not null" json:"owner"`           // GitHub org or user

	// ── github_app credentials (callback-owned; github_app mode only) ──────────
	// Non-secret App/Installation IDs written by the manifest/setup callbacks; zero/omitted in pat mode.
	GithubAppID          int64 `json:"githubAppId,omitempty"`          // App ID (manifest callback)
	GithubInstallationID int64 `json:"githubInstallationId,omitempty"` // Installation ID (setup callback)
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
