package lifecycle

import (
	"time"
)

// TransportState represents the state of the transport (TMS) phase for an artifact per tenant.
type TransportState string

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

// Canonical transport states.
const (
	TransportNotRequested TransportState = "NOT_REQUESTED"
	TransportRequesting   TransportState = "REQUESTING"
	TransportReady        TransportState = "READY"
	TransportFailed       TransportState = "FAILED"
)

// Canonical import states.
const (
	ImportNotStarted ImportState = "NOT_STARTED"
	ImportQueued     ImportState = "QUEUED"
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
	DeployPartial     DeployState = "PARTIAL"  // TODO: not a right situation, remove later
	DeployFailed      DeployState = "FAILED"
	DeployComplete    DeployState = "COMPLETE"
	DeployRollbacking DeployState = "ROLLBACKING"
	DeployRolledBack  DeployState = "ROLLED_BACK"
)

// Aggregate statuses (public surface).
const (
	AggUnknown        AggregateStatus = "UNKNOWN"
	AggPending        AggregateStatus = "PENDING"         // waiting for TR or queued work
	AggAwaitingImport AggregateStatus = "AWAITING_IMPORT" // TR ready, import not started
	AggImporting      AggregateStatus = "IMPORTING"
	AggImportFailed   AggregateStatus = "IMPORT_FAILED"
	AggImported       AggregateStatus = "IMPORTED"
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
