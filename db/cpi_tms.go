package db

import (
	. "mmt-delivery/consts"
	"mmt-delivery/pkg/lifecycle"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// on artifact_tech_id:version can be deployed to *multiple* tenants, so it is better to seperate it into a new table!
// search by artifact_tech_id:version, no need to use ID.
type Artifact struct {
	gorm.Model

	TechID      string `gorm:"index:ux_artifact_tech_version,unique"` // artifact technical id
	Version     string `gorm:"index:ux_artifact_tech_version,unique"`
	Name        string
	PackageID   string       //package techical id
	Type        ArtifactType // iflow, scriptCollection
	Description string
	CreatedBy   string //TODO: may not need it. same above
	CreatedAt   string
	ModifiedBy  string
	ModifiedAt  string
	Status      string // deploy task status. TODO: may not need it. status will be controlled by ArtifactTenantOperation
	TaskId      string // task id. TODO: may not need it. same as above
}

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
// # Design Evolution
//
// Originally this struct was a simple endpoint record: a named pointer to a CPI API URL
// plus a TMS node binding.  RFC 013 upgrades it to a subaccount-centric aggregate that
// owns the full lifecycle of transport prerequisites for one BTP subaccount.
//
// # Lifecycle (LifecycleState)
//
// A tenant progresses through four top-level states:
//
//	DRAFT      → newly created, or key fields changed; no reliable readiness conclusion yet.
//	NOT_READY  → at least one required prerequisite is missing or misconfigured.
//	READYING   → a bootstrap job is actively executing.
//	READY      → all local prerequisites satisfied AND the TMS source node is registered;
//	             the tenant is fully transport-ready and the "Generate TRs" flow is unblocked.
//
// Only the bootstrapper service (via TransitionLifecycle) may advance or regress this field.
// Direct writes from handlers are forbidden.
//
// # Backward Compatibility
//
// The original fields (Name, TransportNodeID/Name/Description, CpiEndpoint, Group) are
// preserved unchanged so that existing API consumers and delivery workflows continue to
// function during the phased migration.  New code should prefer the subaccount fields.
type CpiTenant struct {
	gorm.Model

	// ── Legacy fields (preserved for backward compatibility) ─────────────────────────

	// Name is the human-readable identifier used by existing API consumers and UI.
	// Unique constraint excludes soft-deleted rows.
	Name      string `gorm:"uniqueIndex,where:deleted_at IS NULL"`
	CreatedBy string
	UpdatedBy string

	// TransportNodeID/Name/Description are the original TMS node bindings.
	// Superseded by TmsSourceNodeName (central registration) but retained so that
	// existing DeliveryRule associations continue to resolve correctly.
	TransportNodeID          uint
	TransportNodeName        string
	TransportNodeDescription string

	// CpiEndpoint is the original endpoint record stored as JSON.
	// Superseded by IntegrationSuiteEndpoint but retained for API compatibility.
	CpiEndpoint ApiEndpoint `gorm:"serializer:json"`

	// Group is an informal tag for grouping tenants (e.g. "prod", "ctest", "ep").
	Group string

	// ── Subaccount Identity ───────────────────────────────────────────────────────────

	// SubaccountID is the BTP subaccount GUID that this tenant maps to 1-to-1.
	// It is the primary identity key for all BTP/CF API calls made during bootstrap.
	// Unique constraint excludes soft-deleted rows; duplicate subaccount IDs are rejected with HTTP 409.
	SubaccountID string `gorm:"uniqueIndex:ux_subaccount_id,where:deleted_at IS NULL"`

	// GlobalAccountID is the parent BTP global account; used for entitlement queries.
	GlobalAccountID string

	// Region is the BTP region code of this subaccount (e.g. "eu10", "us10").
	// Determines which CF API domain and BTP endpoints are used during bootstrap.
	Region string

	// CfOrg is the Cloud Foundry organisation GUID; optional, inferred when blank.
	CfOrg string

	// CfSpace is the default CF space where service instances (PIR, CAS) are created.
	// Must exist and be accessible to the bootstrap technical principal.
	CfSpace string

	// ── Integration Suite ─────────────────────────────────────────────────────────────

	// IntegrationSuiteEndpoint is the runtime URL of the Cloud Integration API.
	// This is the canonical endpoint going forward; CpiEndpoint is the legacy alias.
	// Example: "https://<tenant>.it-cpi001.cfapps.eu10.hana.ondemand.com"
	IntegrationSuiteEndpoint string

	// IntegrationSuiteSubscribed is set to true once the bootstrap inspector has
	// confirmed that the subaccount has an active Integration Suite subscription.
	// Subscription must exist before any service instances can be created.
	IntegrationSuiteSubscribed bool

	// ── Local Prerequisite Status ─────────────────────────────────────────────────────
	//
	// Each status field reflects the last known state of one required local resource.
	// Allowed values for all status fields: "missing" | "ready" | "failed"
	//
	// These are updated by the bootstrap inspector and bootstrapper, not by handlers.
	// They exist for fast status-page rendering without re-running a full inspection.

	// PirApiStatus tracks the Process Integration Runtime (api plan) service instance.
	// This instance's service key is used to configure the CloudIntegration destination.
	PirApiStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// CasApplicationStatus tracks the Content Agent Service (application plan) instance.
	// Its service key is the runtime credential for calling the CAS export API.
	CasApplicationStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// CasStandardStatus tracks the Content Agent Service (standard plan) instance.
	// This instance acts as the export worker; its service key configures the
	// ContentAssemblyService destination.
	CasStandardStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// CloudIntegrationDestStatus tracks the BTP destination named "CloudIntegration"
	// in the subaccount's destination service.  Required for CAS to reach the CPI API.
	CloudIntegrationDestStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// ContentAssemblyDestStatus tracks the BTP destination named "ContentAssemblyService"
	// in the subaccount's destination service.  Required for CAS to invoke the assembly worker.
	ContentAssemblyDestStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// TransportManagementDestStatus tracks the BTP destination named per
	// TmsTransportDestinationName in the subaccount's destination service.
	// Required for CAS to push the assembled MTAR package to TMS.
	TransportManagementDestStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// ── Lifecycle ─────────────────────────────────────────────────────────────────────

	// LifecycleState is the single authoritative readiness indicator for this tenant.
	// READY is equivalent to "Transport Ready": all local prerequisites are satisfied
	// AND the TMS source node has been registered in the central TMS context.
	// Only the service layer (TransitionLifecycle) may write this field.
	LifecycleState lifecycle.TenantLifecycleState `gorm:"default:'draft'"`

	// BlockingReason stores the most critical current obstacle as a machine-readable code.
	// Full diagnostic detail lives in the associated TenantBootstrapJob.
	// Example values: "INTEGRATION_SUITE_SUBSCRIPTION_MISSING", "TMS_NODE_MANUAL_REGISTRATION_REQUIRED"
	BlockingReason string

	// ── Central TMS Registration ──────────────────────────────────────────────────────

	// SourceSystemID is the value written into the `sourceSystemId` Additional Property
	// of the TransportManagementService destination during bootstrap.
	//
	// SAP Help reference:
	//   https://help.sap.com/docs/content-agent-service/user-guide/create-transportmanagementservice-destination
	//
	// The destination supports per-content-type overrides:
	//   sourceSystemId.CPI          → used when exporting CPI artifacts
	//   sourceSystemId.APIManagement → used when exporting API Management artifacts
	//   sourceSystemId.sap.build    → used when exporting Build Apps artifacts
	//   sourceSystemId              → default fallback for any content type
	//
	// The value is described as "the ID of the source node of the transport route,
	// for example, DEV_NODE" — which appears to be the TMS node name, not a numeric ID.
	//
	// TODO (Phase 3 implementation): Clarify whether this is identical to
	// TmsSourceNodeName or a distinct value.  If identical, this field can be removed
	// and bootstrap should write TmsSourceNodeName into sourceSystemId.CPI directly.
	SourceSystemID string

	// TmsSourceNodeName is the name of this tenant's source node in the central TMS landscape.
	// Set by CentralTmsRegistrar after successful node create-or-reuse.
	// Used as the "sourceNode" field in every CAS export request body.
	TmsSourceNodeName string

	// TmsNodeRegistrationStatus tracks whether the TMS source node is registered.
	TmsNodeRegistrationStatus lifecycle.PrerequisiteStatus `gorm:"default:'missing'"`

	// TmsTransportDestinationName is the name of the BTP destination in this subaccount
	// that points to the central TMS.  Defaults to "TransportManagementService" (SAP convention).
	// Users may override this if their landscape uses a non-standard destination name.
	// Used as the "transportDestination" field in every CAS export request body.
	// Note: this destination is read by CAS (not cpi-delivery) to push MTAR to TMS.
	// The cpi-delivery app uses CentralTmsContext.TmsApiDestinationName for TMS API calls.
	TmsTransportDestinationName string `gorm:"default:'TransportManagementService'"`

	// CentralTmsContextID is the FK to the CentralTmsContext that owns this tenant's
	// TMS source node.  Set by CentralTmsRegistrar after successful node registration.
	// Nil until the REGISTER_TMS_NODE bootstrap step completes.
	// Multiple CpiTenants belonging to the same subscriber share one CentralTmsContext.
	CentralTmsContextID *uint

	// ── Destination References ────────────────────────────────────────────────────────
	//
	// These fields store the BTP Destination Service destination name for each
	// per-tenant outbound API connection.  Secrets are never stored in the database —
	// they live inside the destination configuration managed by Destination Service.
	//
	// Bootstrap auto-creates these destinations; the name is stored here so the
	// runtime can look them up by name.
	//
	// RFC 008 Phase 5 migration path: in non-SaaS mode the destinations are created
	// in the provider subaccount's Destination Service.  After Phase 5, they move to
	// the subscriber subaccount; the DB field names and runtime lookup code are unchanged.

	// CasEngineDestinationName is the name of the destination used by cpi-delivery to
	// call the CAS engine (content-agent-engine) API at transport time.
	// Default naming: "CPIDELIVERY_CAS_{tenantID}"
	// Used at: pkg/cas Client construction, TrResolver.ResolveTransportRequest.
	CasEngineDestinationName string

	// PirApiDestinationName is the name of the destination used by cpi-delivery to
	// call the CPI API via the PIR api service instance at runtime.
	// Used at: deploy process, artifact/package status queries.
	// Default naming: "CPIDELIVERY_PIR_{tenantID}"
	PirApiDestinationName string

	// LastCredentialRotationAt records when service key credentials were last rotated.
	// Used to enforce rotation policy (at minimum annually; quarterly for high-sensitivity).
	LastCredentialRotationAt *time.Time

	// ── Audit Timestamps ──────────────────────────────────────────────────────────────

	// LastBootstrapAt is the time of the most recent completed bootstrap apply or retry job.
	LastBootstrapAt *time.Time

	// LastValidationAt is the time of the most recent preview or validate job,
	// regardless of whether it succeeded or failed.
	LastValidationAt *time.Time
}

type UaaClaims struct {
	UserName string   `json:"user_name"`
	Scope    []string `json:"scope"`
	jwt.RegisteredClaims
	Origin string `json:"origin"` //maco.accounts400.ondemand.com
	UserID string `json:"user_id"`
	ZoneID string `json:"zid"`
}
