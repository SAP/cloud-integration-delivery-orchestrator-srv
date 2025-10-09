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
