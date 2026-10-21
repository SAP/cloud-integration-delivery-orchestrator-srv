package db

import (
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

type TransportRequest struct {
	ID          int //tr number
	Description string
	Status      string
}

type TransportNode struct {
	ID                   uint   `json:"id"`
	Description          string `json:"description"`
	Name                 string `json:"name"`
	UploadAllowed        bool   `json:"uploadAllowed"`
	NotificationEnabled  bool   `json:"notificationEnabled"`
	ForwardMode          string `json:"forwardMode"`
	ImportDisabled       bool   `json:"importDisabled"`
	ImportDisabledReason string `json:"importDisabledReason"`
	Targets              []struct {
		ID              int    `json:"id"`
		ContentType     string `json:"contentType"`
		DestinationName string `json:"destinationName"`
		ImportOptions   struct {
			Strategy string `json:"strategy"`
		} `json:"importOptions"`
	} `json:"targets"`
	Virtual bool `json:"virtual"`
}

type TransportRoute struct {
	ID           uint   `json:"id"`
	Description  string `json:"description"`
	Name         string `json:"name"`
	SourceNodeID uint   `json:"sourceNodeId"`
	TargetNodeID uint   `json:"targetNodeId"`
}

// ApiEndpoint mirrors the TS interface ApiEndpoint
type ApiEndpoint struct {
	gorm.Model
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// CpiTenant represents a BTP subaccount that has an Integration Suite subscription
// and participates in the CPI Delivery transport workflow.
//
// # Two-layer Bootstrap (RFC 032 P0)
//
// Readiness is achieved through two independent bootstrap flows:
//
//   - Base bootstrap (LifecycleState) — PIR API instance + provider destination.
//     Reaching READY ("Onboarded") unlocks Version Compare, BPMN Diff, deploy,
//     and all PIR-dependent capabilities.
//   - TMS extension (TmsState) — CAS instances, subscriber destinations, TMS node.
//     Reaching READY ("TMS Ready") additionally unlocks TR generation and TMS import.
//
// The two flows have separate state machines, jobs, and endpoints.
// TMS extension depends on base bootstrap being complete (IsOnboarded).
//
// Fields are grouped by owning layer below; each layer's service module is the
// sole writer of its fields.
type CpiTenant struct {
	gorm.Model

	// ── Identity ────────────────────────────────────────────────────────────────────

	// Name is the human-readable identifier used by API consumers and UI.
	Name      string `gorm:"uniqueIndex:ux_cpi_tenants_name,where:deleted_at IS NULL"`
	CreatedBy string
	UpdatedBy string

	// Group is an informal tag for grouping tenants (e.g. "prod", "ctest", "ep").
	Group string

	// ── CF Identity ─────────────────────────────────────────────────────────────────

	CfApiEndpoint string `gorm:"uniqueIndex:ux_cf_identity,where:deleted_at IS NULL"`
	CfOrg         string `gorm:"uniqueIndex:ux_cf_identity,where:deleted_at IS NULL"`
	CfSpace       string

	// ── Base Bootstrap ──────────────────────────────────────────────────────────────
	//
	// Owned by: tenant_bootstrapper.go / tenant_lifecycle.go (TransitionLifecycle).
	// Reaching LifecycleState=ready means "Onboarded": PIR API destination works.

	// LifecycleState is the single authoritative readiness indicator for the base
	// bootstrap (PIR API destination).  READY means the tenant is "Onboarded":
	// PIR API service instance, service key, and provider-side destination are in place.
	//
	// TMS readiness is tracked separately by TmsState.
	// Only the service layer (TransitionLifecycle) may write this field.
	LifecycleState lifecycle.TenantLifecycleState `gorm:"default:'draft'"`

	// BlockingReason stores the most critical current obstacle as a machine-readable code.
	// Only represents base bootstrap (LifecycleState) reasons.
	BlockingReason string

	// PirApiStatus tracks the PIR api service instance (base bootstrap prerequisite).
	PirApiStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// PirApiDestinationName is the provider-side destination for calling the CPI API.
	// Default naming: "CPIDELIVERY_PIR_{tenantID}".  Written by base bootstrap.
	PirApiDestinationName string

	// PirApiUrl is the PIR API root URL stored at bootstrap time for display purposes.
	PirApiUrl string

	// ── TMS Extension (RFC 032 P0) ──────────────────────────────────────────────────
	//
	// Owned by: tms_extension_lifecycle.go / tms_extension_bootstrapper.go.
	// Reaching TmsExtensionState=bootstrapped means "TMS Ready": TR generation and TMS import work.
	// Depends on base bootstrap being complete (IsOnboarded).

	// TmsExtensionState is the authoritative readiness state of the TMS extension.
	// Only the TMS extension lifecycle (transitionTmsWithTx) may write this field.
	TmsExtensionState lifecycle.TmsExtensionState `gorm:"column:tms_extension_state;default:'not_started'"`

	// TmsBlockingReason stores the reason when TmsExtensionState is bootstrap_failed.
	TmsBlockingReason string

	// TMS extension prerequisite statuses (detail states for status-page rendering).
	CasApplicationStatus          lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`
	CasStandardStatus             lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`
	CloudIntegrationDestStatus    lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`
	ContentAssemblyDestStatus     lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`
	TransportManagementDestStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// TMS node binding.
	TmsSourceNodeName   string
	TmsSourceNodeID     uint
	CentralTmsContextID *uint

	// CasEngineDestinationName is the provider-side destination for calling CAS.
	// Default naming: "CPIDELIVERY_CAS_{tenantID}".  Written by TMS extension bootstrap.
	CasEngineDestinationName string

	// TmsNodeRegistrationStatus is superseded by TmsState.
	// Deprecated: retained only so the DB column is not orphaned by GORM.
	TmsNodeRegistrationStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// ── Audit ───────────────────────────────────────────────────────────────────────

	LastCredentialRotationAt *time.Time
	LastBootstrapAt          *time.Time
	LastValidationAt         *time.Time
}

type UaaClaims struct {
	UserName string   `json:"user_name"`
	Scope    []string `json:"scope"`
	jwt.RegisteredClaims
	Origin string `json:"origin"` //example.accounts400.ondemand.com
	UserID string `json:"user_id"`
	ZoneID string `json:"zid"`
}

// CfApiURL returns the CF API root URL for this tenant.
// It is the direct value of CfApiEndpoint, provided by the Operator at registration.
func (t *CpiTenant) CfApiURL() string {
	return t.CfApiEndpoint
}

// IsOnboarded returns true if the tenant has completed the base bootstrap
// and has a working PIR API destination.
func (t *CpiTenant) IsOnboarded() bool {
	return t.LifecycleState == lifecycle.TenantReady && t.PirApiDestinationName != ""
}

// IsTmsReady returns true if the TMS extension is fully configured and ready.
func (t *CpiTenant) IsTmsReady() bool {
	return t.TmsExtensionState == lifecycle.TmsBootstrapped
}
