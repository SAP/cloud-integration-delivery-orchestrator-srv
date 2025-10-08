package db

import (
	"mmt-delivery/pkg/lifecycle"

	"gorm.io/gorm"
)

type DeliveryRequest struct {
	gorm.Model
	Name string

	JiraLink        string                    // related Jira ticket URL
	AggregateStatus lifecycle.AggregateStatus // pending, in-progress, completed, failed

	// One-to-many: a delivery request has many artifacts
	ArtifactTenantOperations []ArtifactTenantOperation `gorm:"foreignKey:DeliveryRequestID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	SourceTenantID *uint
	SourceTenant   *CpiTenant `gorm:"foreignKey:SourceTenantID"`

	DeliveryRuleID *uint
	DeliveryRule   *DeliveryRule `gorm:"foreignKey:DeliveryRuleID"`

	TargetNodes  []TransportNode  `gorm:"serializer:json"`
	TargetRoutes []TransportRoute `gorm:"serializer:json"`

	CreatedBy string
	UpdatedBy string
}
