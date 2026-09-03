package db

import (
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// TenantBootstrapJob records a single bootstrap attempt against a CpiTenant.
//
// # Design Intent
//
// Bootstrap is not an instantaneous operation — it involves up to ~8 sequential
// remote steps (subscription check, CF space validation, service instance creation,
// destination creation, TMS node registration, connectivity validation).  Any step
// can fail for different reasons: missing user action, permission blocks, remote API
// errors, or config mismatches.  Rather than encoding all of that transient detail
// into the long-lived CpiTenant record, we isolate it here in a dedicated job row.
//
// One CpiTenant may accumulate many jobs over its lifetime (preview → apply → retry…).
// Callers should always query the most-recent job for the current status display.
//
// # Job Types
//
//   - "preview"  — read-only inspection; identifies what is missing without creating anything.
//   - "apply"    — creates every missing prerequisite in sequence; runs asynchronously.
//   - "retry"    — resumes an "apply" from the step it last failed at.
//   - "validate" — re-checks readiness after a user has manually completed a prerequisite
//     (e.g. subscribing Integration Suite or creating a TMS node via the TMS UI).
//
// # Job States
//
//   - "running"             — goroutine is actively executing steps.
//   - "waiting_user_action" — execution paused; a human must complete a prerequisite
//     (e.g. subscribe to Integration Suite, create TMS node in TMS UI).
//   - "partially_applied"   — some steps succeeded before a failure; partial state exists
//     in the target subaccount.  Retry is safe because all steps are idempotent.
//   - "failed"              — terminal failure; see FailureType + CurrentStep for diagnosis.
//   - "finished"            — all required steps completed; CpiTenant.LifecycleState → READY.
//
// # JSON Payload Columns
//
// The four *JSON columns capture a snapshot of what this job found/did, without
// writing raw secrets.  They are intentionally denormalised so that an operator can
// diagnose any past job without joining other tables.
//
//   - MissingPrerequisites  — list of prerequisite items that were absent at job start.
//   - PermissionFindings    — list of items where the bootstrap principal lacked permissions.
//   - CredentialActions     — list of {destinationName, actionType} pairs for every
//     Destination or service key action taken.  Raw secrets are NEVER stored here.
//   - CentralRegistration   — TMS node create/reuse result (name, id, status).
type TenantBootstrapJob struct {
	gorm.Model

	// CpiTenantID is the owning tenant.  FK to cpi_tenants.id.
	// Indexed for fast "latest job for tenant X" queries.
	CpiTenantID uint `gorm:"index;not null"`

	// JobType distinguishes why this job was created.
	JobType lifecycle.BootstrapJobType `gorm:"not null"`

	// State is the current lifecycle position of this job.
	State lifecycle.BootstrapJobState `gorm:"not null;default:'running'"`

	// CurrentStep is the bootstrap step that is executing or last executed.
	// Follows the step enum defined in RFC 013 §02:
	//   CHECK_SUBSCRIPTION → CHECK_SPACE_CONTEXT → CHECK_PIR_API →
	//   CHECK_CAS_APPLICATION → CHECK_CAS_STANDARD → CHECK_SERVICE_KEYS →
	//   CHECK_DESTINATIONS → REGISTER_TMS_NODE → VALIDATE_TRANSPORT_READY
	CurrentStep string

	// FailureType classifies why the job entered a non-FINISHED terminal state.
	// Empty when State is JobRunning or JobFinished.
	FailureType lifecycle.BootstrapFailureType

	// ErrorDetail stores the raw error message from the step that caused this job
	// to fail.  Set only when State is failed or waiting_user_action.
	// This is the human-readable diagnosis that persists across retries,
	// complementing the machine-readable FailureType + CurrentStep codes.
	// Example: "create service key (PIR_API): CF API returned 403 Forbidden: CF-NotAuthorized"
	ErrorDetail string

	// MissingPrerequisites is a JSON snapshot of every prerequisite item that was absent
	// when this job started its inspection phase.  Shape: []string (blocking reason codes).
	// Example: ["CAS_APPLICATION_MISSING", "CLOUD_INTEGRATION_DEST_MISSING"]
	MissingPrerequisites datatypes.JSON

	// PermissionFindings lists items where the bootstrap technical principal lacked the
	// required BTP/CF permissions.  Stored so operators can grant the right roles without
	// needing to re-run a full preview.
	// Shape: []string (blocking reason codes).
	PermissionFindings datatypes.JSON

	// CredentialActions records every service key and Destination Service action taken during
	// this job: service key creation/rotation, Destination creation/update.
	// Only action metadata is stored — raw secrets are NEVER written here or anywhere in the
	// business database.  Credentials live exclusively in BTP Destination Service.
	// Shape: []{DestinationName string, ActionType string}
	// Example: [{"destinationName":"CPIDELIVERY_CAS_42","actionType":"created"}]
	CredentialActions datatypes.JSON

	// CentralRegistration captures the outcome of the TMS source node create-or-reuse step.
	// Shape: {NodeName string, NodeID string, Status string, Action string}
	// Action is one of: "created" | "reused" | "waiting_user_action" | "config_mismatch"
	CentralRegistration datatypes.JSON

	// StartedAt is set when the job row is first created (before the goroutine fires).
	StartedAt time.Time

	// EndedAt is set when the job reaches a terminal state (finished, failed, waiting_user_action).
	// Nil while the job is running or partially applied.
	EndedAt *time.Time
}
