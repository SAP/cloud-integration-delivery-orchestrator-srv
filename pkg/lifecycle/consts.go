package lifecycle

import (
	"slices"
	"strings"
)

// RequestState represents the state of the transport (TMS) phase for an artifact per tenant.
type RequestState string

// ImportState represents the state of the design-time import phase.
type ImportState string

// DeployState represents the state of the runtime deploy phase.
type DeployState string

// AggregateStatus is the externally exposed rolled-up status at the artifact (or batch) level.
type AggregateStatus string

type ConditionState string

// Canonical Delivery Request states.
const (
	RequestPending  RequestState = "NOT_REQUESTED"
	RequestStarting RequestState = "REQUESTING"
	RequestReady    RequestState = "READY"
	RequestFailed   RequestState = "FAILED"
)

// Canonical import states.
// TMS node status mapping: INITIAL, RUNNING, SUCCEEDED, WARNING, FATAL, REPEAT, etc.
const (
	ImportNotStarted ImportState = "NOT_STARTED"
	ImportQueued     ImportState = "QUEUED" // INITIAL
	ImportDisabled   ImportState = "IMPORT_DISABLED"
	ImportInProgress ImportState = "IN_PROGRESS"
	ImportFailed     ImportState = "FAILED"
	ImportComplete   ImportState = "COMPLETE"
)

// Canonical deploy states.
const (
	DeployNotStarted DeployState = "NOT_STARTED"
	DeployQueued     DeployState = "QUEUED"
	DeployDisabled   DeployState = "DEPLOY_DISABLED"
	DeployInProgress DeployState = "IN_PROGRESS"
	DeployFailed     DeployState = "FAILED"
	DeployComplete   DeployState = "COMPLETE"
)

// Aggregate statuses (public surface).
const (
	AggUnknown        AggregateStatus = "UNKNOWN"
	AggPending        AggregateStatus = "PENDING"          // waiting for TR or queued work
	AggWaitingApprove AggregateStatus = "WAITING_APPROVAL" // waiting for manual approval to import
	AggInProgress     AggregateStatus = "IN_PROGRESS"
	AggFailed         AggregateStatus = "FAILED"

	AggAwaitingImport AggregateStatus = "AWAITING_IMPORT" // TR ready, import not started
	AggImporting      AggregateStatus = "IMPORTING"
	AggImportFailed   AggregateStatus = "IMPORT_FAILED"
	AggWaitingDeploy  AggregateStatus = "AWAITING_DEPLOY" // import done, deploy not started
	AggDeploying      AggregateStatus = "DEPLOYING"
	AggDeployFailed   AggregateStatus = "DEPLOY_FAILED"
	AggDeployed       AggregateStatus = "DEPLOYED"
	AggCanceled       AggregateStatus = "CANCELED"
)

// Condition type constants (non-exhaustive). Keeping as strings for flexibility.
const (
	CondError   ConditionState = "Error"
	CondWarn    ConditionState = "Warn"
	CondSuccess ConditionState = "Success"
)

// ── RFC 013: Tenant Bootstrap Types ──────────────────────────────────────────

// TenantLifecycleState is the top-level readiness state of a CpiTenant.
// It is the single authoritative indicator of whether a tenant can participate
// in the transport workflow.  Only the service layer (TransitionLifecycle) may
// write this field; handlers must not modify it directly.
type TenantLifecycleState string

const (
	// TenantDraft is set on creation, or whenever a key subaccount field is
	// changed (SubaccountID, Region, CfSpace, IntegrationSuiteEndpoint).
	// It means no reliable readiness conclusion exists yet.
	TenantDraft TenantLifecycleState = "draft"

	// TenantNotReady means the most recent inspection found at least one
	// blocking prerequisite.  The BlockingReason field explains what.
	TenantNotReady TenantLifecycleState = "not_ready"

	// TenantReadying means a bootstrap apply or retry job is actively running.
	TenantReadying TenantLifecycleState = "readying"

	// TenantReady means all local prerequisites are satisfied AND the TMS source
	// node is registered in the central TMS context.  This is equivalent to
	// "Transport Ready" — the Generate TRs flow is unblocked.
	TenantReady TenantLifecycleState = "ready"
)

// BootstrapJobType describes why a TenantBootstrapJob was created.
type BootstrapJobType string

const (
	JobTypePreview  BootstrapJobType = "preview"  // read-only inspection; no side effects
	JobTypeApply    BootstrapJobType = "apply"     // creates all missing prerequisites
	JobTypeRetry    BootstrapJobType = "retry"     // resumes apply from the last failed step
	JobTypeValidate BootstrapJobType = "validate"  // re-checks readiness after manual operator action
)

// BootstrapJobState is the execution state of a TenantBootstrapJob.
type BootstrapJobState string

const (
	// JobRunning means the goroutine is actively executing steps.
	JobRunning BootstrapJobState = "running"

	// JobWaitingUserAction means execution is paused; a human must complete a
	// prerequisite (e.g. subscribe to Integration Suite, create TMS node in TMS UI)
	// before the job can continue via a validate or retry call.
	JobWaitingUserAction BootstrapJobState = "waiting_user_action"

	// JobPartiallyApplied means some steps succeeded before a failure occurred.
	// Partial state may exist in the target subaccount; retry is safe because all
	// steps are idempotent (create-or-reuse semantics).
	JobPartiallyApplied BootstrapJobState = "partially_applied"

	// JobFailed is a terminal failure state.  See FailureType and CurrentStep for
	// diagnosis.
	JobFailed BootstrapJobState = "failed"

	// JobFinished means all required steps completed successfully.
	// The owning CpiTenant.LifecycleState transitions to TenantReady.
	JobFinished BootstrapJobState = "finished"
)

// BootstrapFailureType classifies the root cause when a bootstrap job does not
// reach JobFinished.  Used to determine the correct remediation path.
type BootstrapFailureType string

const (
	// FailureWaitingUserAction: a human must complete a manual prerequisite before
	// the bootstrap can continue (e.g. subscribe Integration Suite, create TMS node).
	FailureWaitingUserAction BootstrapFailureType = "waiting_user_action"

	// FailurePermissionBlocked: the bootstrap technical principal lacked the required
	// BTP/CF permissions.  An admin must grant the appropriate roles.
	FailurePermissionBlocked BootstrapFailureType = "permission_blocked"

	// FailureRemoteSystemError: a remote API (CF, BTP, TMS) returned an unexpected error.
	// Retry may succeed once the remote system recovers.
	FailureRemoteSystemError BootstrapFailureType = "remote_system_error"

	// FailureConfigMismatch: a required resource already exists but its configuration
	// does not match what bootstrap expects (e.g. wrong destination URL, wrong node type).
	// Requires human review before retry — bootstrap will not overwrite existing resources.
	FailureConfigMismatch BootstrapFailureType = "config_mismatch"
)

// PrerequisiteStatus is the last-known state of a single local bootstrap prerequisite
// (service instance, service key, or BTP destination) stored on CpiTenant.
type PrerequisiteStatus string

const (
	PrereqMissing PrerequisiteStatus = "missing" // resource does not exist yet
	PrereqReady   PrerequisiteStatus = "ready"   // resource exists and is correctly configured
	PrereqFailed  PrerequisiteStatus = "failed"  // resource exists but is in an error state
)

// DeriveImport maps TMS transport node state to our ImportState.
// WARNING is treated as successful import (deploy may proceed); REPEAT means TR was reset and may be imported again.
func DeriveImport(state string) ImportState {
	switch strings.ToUpper(state) {
	case "INITIAL":
		return ImportQueued
	case "RUNNING":
		return ImportInProgress
	case "SUCCEEDED", "WARNING":
		return ImportComplete
	case "REPEAT":
		return ImportQueued
	case "FATAL", "FAILED", "ERROR":
		return ImportFailed
	default:
		return ImportNotStarted
	}
}

// NOTE: should remove ops that is in source tenant before calling this function!
func DeriveAggregateStatus(aggStatus AggregateStatus, importStates []ImportState, deployStates []DeployState) AggregateStatus {
	// Short-circuit for explicit terminal/carry-over states
	switch aggStatus {
	case AggCanceled:
		return AggCanceled
	}

	// If no ops provided, keep current aggregate status
	if len(importStates) == 0 && len(deployStates) == 0 {
		if aggStatus != "" {
			return aggStatus
		}
		return AggUnknown
	}

	// Helpers
	anyImport := func(vals []ImportState, targets ...ImportState) bool {
		for _, v := range vals {
			if slices.Contains(targets, v) {
				return true
			}
		}
		return false
	}
	allImport := func(vals []ImportState, allowed ...ImportState) bool {
		if len(vals) == 0 {
			return false
		}
		for _, v := range vals {
			ok := slices.Contains(allowed, v)
			if !ok {
				return false
			}
		}
		return true
	}
	anyDeploy := func(vals []DeployState, targets ...DeployState) bool {
		for _, v := range vals {
			if slices.Contains(targets, v) {
				return true
			}
		}
		return false
	}
	allDeploy := func(vals []DeployState, allowed ...DeployState) bool {
		if len(vals) == 0 {
			return false
		}
		for _, v := range vals {
			ok := slices.Contains(allowed, v)
			if !ok {
				return false
			}
		}
		return true
	}

	// Import phase precedence
	if anyImport(importStates, ImportFailed) {
		return AggImportFailed
	}
	if anyImport(importStates, ImportQueued) {
		return AggAwaitingImport
	}
	if anyImport(importStates, ImportInProgress) {
		return AggImporting
	}

	// Consider ImportDisabled as effectively complete for progressing to deploy
	importsComplete := allImport(importStates, ImportComplete, ImportDisabled)

	// Deploy phase precedence (only meaningful once imports are complete/disabled)
	if importsComplete {
		// Check for deployment failures
		if anyDeploy(deployStates, DeployFailed) {
			return AggDeployFailed
		}
		if anyDeploy(deployStates, DeployInProgress) {
			return AggDeploying
		}
		// Consider DeployDisabled as effectively complete
		if allDeploy(deployStates, DeployComplete, DeployDisabled) {
			return AggDeployed
		}
		// Imports done but deploy not yet started
		return AggWaitingDeploy
	}

	// If we've been approved and imports haven't started yet, reflect awaiting import
	switch aggStatus {
	case AggAwaitingImport, AggWaitingApprove, AggPending:
		return aggStatus
	}

	// Fallback if states are mixed but don't hit explicit rules
	return AggInProgress
}
