package db

import (
	"mmt-delivery/consts"
	"time"

	"gorm.io/gorm"
)

// VersionCompareSnapshot stores the latest version comparison snapshot for a Delivery Rule.
// At most one record per DeliveryRuleID (upsert pattern).
type VersionCompareSnapshot struct {
	gorm.Model
	DeliveryRuleID uint `gorm:"uniqueIndex"`
	Status         consts.SnapshotStatus
	TriggeredAt    time.Time
	CompletedAt    *time.Time
	TriggeredBy    string
	Data           SnapshotData `gorm:"serializer:json"`
	Error          string
}

// SnapshotData is the JSON-serialized payload stored in VersionCompareSnapshot.Data.
// It contains raw version data only — match computation happens at query time.
type SnapshotData struct {
	SourceTenantID  uint              `json:"sourceTenantID"`
	ComparedTenants []uint            `json:"comparedTenants"` // tenant IDs
	Packages        []PackageSnapshot `json:"packages"`
}

// PackageSnapshot holds all artifact snapshots within a single CPI package.
type PackageSnapshot struct {
	PackageID string             `json:"packageID"`
	Artifacts []ArtifactSnapshot `json:"artifacts"`
}

// ArtifactSnapshot holds version info for a single artifact across all tenants.
type ArtifactSnapshot struct {
	ID       string                       `json:"id"`
	Name     string                       `json:"name"`
	Type     string                       `json:"type"`     // "Integration Flow" | "Script Collection"
	Versions map[uint]ArtifactVersionInfo `json:"versions"` // key = tenant ID (includes source tenant)
}

// ArtifactVersionInfo holds the design-time and runtime version for one artifact on one tenant.
type ArtifactVersionInfo struct {
	DesignTimeVersion string `json:"designTimeVersion"`    // "active" means DRAFT
	ModifiedBy        string `json:"modifiedBy,omitempty"` // last design-time committer (from CPI API)
	ModifiedAt        string `json:"modifiedAt,omitempty"` // last design-time modification time (from CPI API)
	RuntimeVersion    string `json:"runtimeVersion"`
	RuntimeStatus     string `json:"runtimeStatus"` // STARTED | STARTING | ERROR
	Error             string `json:"error,omitempty"`
}
