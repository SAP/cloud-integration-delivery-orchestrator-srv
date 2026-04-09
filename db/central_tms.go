package db

import (
	"time"

	"gorm.io/gorm"
)

// CentralTmsContext represents the TMS subscription subaccount belonging to one
// subscriber.  It is the hub of the hub-and-spoke transport topology:
//
//   - Each CpiTenant (spoke) is a Cloud Integration subaccount with local
//     prerequisites (PIR, CAS service instances, three destinations for CAS).
//   - Each CentralTmsContext (hub) is the TMS subaccount for that subscriber,
//     owning the transport landscape and source node registry.
//
// # Subscriber Resource Layers
//
// A subscriber brings two independent resource layers:
//
//  1. CPI Tenants — one BTP subaccount per environment (dev, ctest, prod…).
//     Each has CAS standard, CAS application, and PIR api service instances,
//     plus ContentAssemblyService, CloudIntegration, and TransportManagementService
//     destinations inside the subaccount.
//
//  2. TMS Instance — one TMS subscription in any BTP subaccount (not necessarily
//     a CPI tenant subaccount).  All CPI tenants for this subscriber register
//     their source nodes here and route transports through it.
//
// # Two Kinds of TMS Destination
//
// The TMS resource appears as two different destinations in the system, serving
// completely different callers:
//
//   - TransportManagementService (inside each CPI tenant subaccount)
//     → read by CAS automatically to push the MTAR package to TMS.
//     → created by tenant local bootstrap.
//
//   - TmsApiDestinationName (in subscriber's primary subaccount)
//     → read by cpi-delivery app to call TMS import / status APIs.
//     → provided by the subscriber; stored on this struct.
//
// # V1 / SaaS Compatibility
//
// v1 is a single-subscriber deployment — there is only one CentralTmsContext row.
// The data model does not hard-code singleton semantics: CpiTenant references this
// table via CentralTmsContextID, allowing multiple contexts to exist for multi-
// subscriber SaaS deployments (RFC 008).
//
// SubscriberZoneID is empty in v1 and populated per-subscriber in SaaS mode.
//
// # NodeManagementApiAvailable Gate
//
// TMS exposes a node management REST API, but its availability is not guaranteed
// across all configurations.  This flag controls whether the bootstrapper takes the
// automatic path (API create-or-reuse) or the guided-operator path (generate a
// recommended node name, set job state to "waiting_user_action", wait for manual
// creation in TMS UI, then re-validate).
type CentralTmsContext struct {
	gorm.Model

	// SubscriberZoneID is the BTP zone ID (zid claim) of the subscribing tenant.
	// Empty in v1 single-subscriber deployments.
	// Populated in SaaS mode (RFC 008) to isolate records per subscriber.
	SubscriberZoneID string

	// DisplayName is a human-readable label for operator use.
	// Example: "MMT Central TMS (eu10)"
	DisplayName string

	// SubaccountID is the BTP subaccount GUID where the TMS service is subscribed.
	// This is the TMS subaccount, not a CPI tenant subaccount.
	SubaccountID string

	// Region is the BTP region of the TMS subaccount.
	// Example: "eu10"
	Region string

	// TmsApiEndpoint is the root URL of the TMS REST API.
	// Example: "https://transport.cfapps.eu10.hana.ondemand.com"
	// Used by CentralTmsRegistrar when calling node management endpoints.
	TmsApiEndpoint string

	// TmsApiDestinationName is the name of the BTP destination used by cpi-delivery
	// to call TMS APIs (import, status queries, node management).
	// The destination is provided by the subscriber and lives in their primary
	// subaccount's Destination Service.
	// RFC 008 Phase 5 standard name: "tms"
	// Raw credentials are never stored in the business database.
	TmsApiDestinationName string

	// DefaultNodeNamePattern is a template for auto-generating TMS source node names
	// during bootstrap.  Supported variables:
	//   {TenantName}   — CpiTenant.Name
	//   {SubaccountID} — CpiTenant.SubaccountID (truncated to TMS name length limits)
	//   {Region}       — CpiTenant.Region
	// Default: "CI_{TenantName}"  (aligns with existing PoC convention: NODE_MMT_CF_DEV)
	DefaultNodeNamePattern string

	// NodeManagementApiAvailable records whether the TMS node management REST API
	// has been confirmed reachable and usable for this context.
	// - true  → bootstrapper creates-or-reuses source nodes automatically via API.
	// - false → bootstrapper falls back to guided-operator mode: generates a recommended
	//           node name, sets the job state to "waiting_user_action", and waits for
	//           the operator to create the node manually in TMS UI before re-validating.
	// Set by a human after the pre-Phase-3 API availability spike (RFC 013 §05).
	NodeManagementApiAvailable bool

	// LastValidatedAt records when the connectivity between cpi-delivery and the
	// TMS API endpoint was last successfully verified.  Nil until first validation.
	LastValidatedAt *time.Time
}
