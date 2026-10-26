package db

import (
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// TenantBootstrapJob records a single bootstrap attempt against a CpiTenant.
//
// One CpiTenant may accumulate many jobs over its lifetime.  Callers query the
// most-recent job for the current status display.
//
// Core fields: State (running/finished/failed), CurrentStep (progress),
// ErrorDetail (failure diagnosis).  MissingPrerequisites captures what was
// absent at job start; CredentialActions is an audit trail of resources created.
type TenantBootstrapJob struct {
	gorm.Model

	// CpiTenantID is the owning tenant.  FK to cpi_tenants.id.
	CpiTenantID uint `gorm:"index;not null"`

	// Layer distinguishes tenant bootstrap jobs (BASE) from TMS extension
	// bootstrap jobs (TMS).  Empty string indicates a legacy job created before
	// the P0 split.
	Layer lifecycle.BootstrapLayer

	// JobType is always "apply" (§2.1.1: no retry/preview/validate distinction).
	JobType lifecycle.BootstrapJobType `gorm:"not null"`

	// State is the current lifecycle position of this job.
	State lifecycle.BootstrapJobState `gorm:"not null;default:'running'"`

	// CurrentStep is the bootstrap step that is executing or last executed.
	// BASE: CHECK_PIR_API → ENSURE_PIR_PROVIDER_DEST
	// TMS:  CHECK_CAS_APPLICATION → CHECK_CAS_STANDARD → CHECK_DESTINATION_SERVICE →
	//       CHECK_DESTINATIONS → ENSURE_CAS_PROVIDER_DEST
	CurrentStep string

	// FailureType classifies why the job entered a non-FINISHED terminal state.
	// Empty when State is JobRunning or JobFinished.
	FailureType lifecycle.BootstrapFailureType

	// ErrorDetail stores the raw error message from the step that caused this job
	// to fail.  Human-readable diagnosis complementing FailureType + CurrentStep.
	ErrorDetail string

	// MissingPrerequisites is a JSON snapshot of prerequisite items absent at job start.
	// Shape: []string (missing item codes).
	MissingPrerequisites datatypes.JSON

	// CredentialActions records service key and Destination actions taken during this job.
	// Only action metadata — raw secrets are NEVER stored.
	// Shape: []{DestinationName string, ActionType string}
	CredentialActions datatypes.JSON

	// StartedAt is set when the job row is first created.
	StartedAt time.Time

	// EndedAt is set when the job reaches a terminal state.
	EndedAt *time.Time
}
