package db

import (
	"time"

	"gorm.io/gorm"
)

// CentralTmsContext holds the provider-side TMS configuration for cpi-delivery.
//
// The TMS subscription lives in the operator's BTP subaccount.  Its OAuth
// credentials are stored as a BTP destination in the provider Destination
// Service.  This record stores only the destination name (the pointer) and an
// audit timestamp — raw credentials are never written to the business database.
//
// # Two Kinds of TMS Destination
//
// The TMS resource appears as two different destinations in the system:
//
//   - TmsApiDestinationName (in the provider subaccount Destination Service)
//     → read by cpi-delivery to call TMS import / status / node APIs.
//     → provided by the operator; referenced by this record.
//
//   - TransportManagementService (in each CPI tenant subscriber subaccount)
//     → read by CAS to push the assembled MTAR package to TMS.
//     → created by tenant bootstrap, credentials copied from TmsApiDestinationName.
//
// # Bootstrap Prerequisite
//
// CentralTmsContext is a hard prerequisite for tenant bootstrap.  If no record
// exists or TmsApiDestinationName is empty, CHECK_CENTRAL_TMS_CONTEXT blocks
// bootstrap with WaitingUserAction = "CENTRAL_TMS_NOT_CONFIGURED".
//
// # V1 Singleton
//
// v1 is a single-operator deployment — there is exactly one row.
type CentralTmsContext struct {
	gorm.Model

	// TmsApiDestinationName is the name of the BTP destination used by cpi-delivery
	// to call TMS APIs (import, status queries, node management).
	// The destination lives in the provider subaccount's Destination Service.
	// Its URL field is the TMS API endpoint; its OAuth credentials are used both
	// for direct TMS API calls and to populate each tenant's TransportManagementService
	// destination during bootstrap.
	TmsApiDestinationName string

	// LastValidatedAt records when the connectivity between cpi-delivery and the
	// TMS API endpoint was last successfully verified.  Nil until first validation.
	LastValidatedAt *time.Time
}
