package lifecycle

import (
	"time"
)

// RequestState represents the state of the transport (TMS) phase for an artifact per tenant.
type RequestState string

// ImportState represents the state of the design-time import phase.
type ImportState string

// DeployState represents the state of the runtime deploy phase.
type DeployState string

// AggregateStatus is the externally exposed rolled-up status at the artifact (or batch) level.
type AggregateStatus string

// Condition models a discrete boolean fact about an artifact or operation, inspired by Kubernetes conditions.
type Condition struct {
	Type               string    `json:"type"`   // e.g. TransportReady, PartialProgress
	Status             string    `json:"status"` // "True" | "False" | "Unknown"
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

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
	ImportInProgress ImportState = "IN_PROGRESS"
	ImportPartial    ImportState = "PARTIAL" // TODO: not a right situation, remove later
	ImportFailed     ImportState = "FAILED"
	ImportComplete   ImportState = "COMPLETE"
)

// Canonical deploy states.
const (
	DeployNotStarted  DeployState = "NOT_STARTED"
	DeployQueued      DeployState = "QUEUED"
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
	AggAwaitingImport AggregateStatus = "AWAITING_IMPORT"  // TR ready, import not started
	AggImporting      AggregateStatus = "IMPORTING"
	AggImportFailed   AggregateStatus = "IMPORT_FAILED"
	AggImported       AggregateStatus = "IMPORTED"
	AggWaitingDeploy  AggregateStatus = "AWAITING_DEPLOY" // import done, deploy not started
	AggDeploying      AggregateStatus = "DEPLOYING"
	AggDeployFailed   AggregateStatus = "DEPLOY_FAILED"
	AggDeployed       AggregateStatus = "DEPLOYED"
	AggRollbacking    AggregateStatus = "ROLLBACKING"
	AggRolledBack     AggregateStatus = "ROLLED_BACK"
	AggCanceled       AggregateStatus = "CANCELED"
)

// Condition type constants (non-exhaustive). Keeping as strings for flexibility.
const (
	CondTransportReady    = "TransportReady"
	CondImportComplete    = "ImportComplete"
	CondDeployComplete    = "DeployComplete"
	CondPartialProgress   = "PartialProgress"
	CondRetryScheduled    = "RetryScheduled"
	CondRollbackInProgess = "RollbackInProgress"
	CondCanceled          = "Canceled"
	CondLastFailurePhase  = "LastFailurePhase"  // Reason holds phase name
	CondLastFailureReason = "LastFailureReason" // Reason holds error code
)
