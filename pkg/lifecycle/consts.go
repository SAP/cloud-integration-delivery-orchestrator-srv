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
// status from TMS API: INITIAL, RUNNING, SUCCEEDED, FATAL, etc...
const (
	ImportNotStarted ImportState = "NOT_STARTED"
	ImportQueued     ImportState = "QUEUED" // INITIAL
	ImportDisabled   ImportState = "IMPORT_DISABLED"
	ImportInProgress ImportState = "IN_PROGRESS"
	ImportPartial    ImportState = "PARTIAL" // TODO: not a right situation, remove later
	ImportFailed     ImportState = "FAILED"
	ImportComplete   ImportState = "COMPLETE"
)

// Canonical deploy states.
const (
	DeployNotStarted  DeployState = "NOT_STARTED"
	DeployQueued      DeployState = "QUEUED"
	DeployDisabled    DeployState = "DEPLOY_DISABLED"
	DeployInProgress  DeployState = "IN_PROGRESS"
	DeployPartial     DeployState = "PARTIAL" // TODO: not a right situation, remove later
	DeployFailed      DeployState = "FAILED"
	DeployComplete    DeployState = "COMPLETE"
	DeployRollbacking DeployState = "ROLLBACKING"
	DeployRolledBack  DeployState = "ROLLED_BACK"
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

// INITIAL, RUNNING, SUCCEEDED, FATAL
func DeriveImport(state string) ImportState {
	switch strings.ToUpper(state) {
	case "INITIAL":
		return ImportQueued
	case "RUNNING":
		return ImportInProgress
	case "SUCCEEDED":
		return ImportComplete
	case "FATAL", "FAILED", "ERROR":
		return ImportFailed
	case "PARTIAL":
		return ImportPartial
	default:
		return ImportNotStarted
	}
}

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
	if anyImport(importStates, ImportInProgress, ImportQueued) {
		return AggImporting
	}

	// Consider ImportDisabled as effectively complete for progressing to deploy
	importsComplete := allImport(importStates, ImportComplete, ImportDisabled)

	// Deploy phase precedence (only meaningful once imports are complete/disabled)
	if importsComplete {
		// Failure first, including rolled back
		if anyDeploy(deployStates, DeployFailed) || anyDeploy(deployStates, DeployRolledBack) {
			return AggDeployFailed
		}
		if anyDeploy(deployStates, DeployInProgress, DeployQueued, DeployRollbacking) {
			return AggDeploying
		}
		if allDeploy(deployStates, DeployComplete) {
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
