package lifecycle

import (
	"strings"
	"time"
)

// DeriveAggregate computes an AggregateStatus from component phase states and conditions.
// Priority ordering (highest first):
//
//	Rollbacking, RolledBack, Canceled, DeployFailed, Deploying, Deployed,
//	ImportFailed, Importing, Imported, AwaitingImport, Pending, Unknown.
func DeriveAggregate(t RequestState, i ImportState, d DeployState, conds []Condition) AggregateStatus {
	ci := indexConds(conds)

	if isTrue(ci, CondRollbackInProgess) || d == DeployRollbacking {
		return AggRollbacking
	}
	if d == DeployRolledBack {
		return AggRolledBack
	}
	if isTrue(ci, CondCanceled) {
		return AggCanceled
	}

	// Deploy phase
	if d == DeployFailed {
		return AggDeployFailed
	}
	if d == DeployInProgress || d == DeployPartial {
		return AggDeploying
	}
	if d == DeployComplete {
		return AggDeployed
	}

	// Import phase
	if i == ImportFailed {
		return AggImportFailed
	}
	if i == ImportInProgress || i == ImportPartial {
		return AggImporting
	}
	if i == ImportComplete {
		return AggImported
	}

	// Transport ready but import not started
	if t == RequestReady {
		return AggAwaitingImport
	}

	if t == RequestPending || t == RequestStarting {
		return AggPending
	}

	return AggUnknown
}

func indexConds(conds []Condition) map[string]Condition {
	m := make(map[string]Condition, len(conds))
	for _, c := range conds {
		m[c.Type] = c
	}
	return m
}

func isTrue(index map[string]Condition, key string) bool {
	c, ok := index[key]
	return ok && c.Status == "True"
}

// MergeOrUpdateCondition inserts or updates a condition preserving LastTransitionTime semantics.
func MergeOrUpdateCondition(existing []Condition, newCond Condition, now time.Time) []Condition {
	updated := false
	for i := range existing {
		if existing[i].Type == newCond.Type {
			// Transition detection
			if existing[i].Status != newCond.Status {
				newCond.LastTransitionTime = now
			} else {
				newCond.LastTransitionTime = existing[i].LastTransitionTime
			}
			existing[i] = newCond
			updated = true
			break
		}
	}
	if !updated {
		if newCond.LastTransitionTime.IsZero() {
			newCond.LastTransitionTime = now
		}
		existing = append(existing, newCond)
	}
	return existing
}

// LegacyCodeFromAggregate provides backward compatibility with original integer codes.
// NOTE: This is a lossy mapping and may collapse nuanced states.
func LegacyCodeFromAggregate(a AggregateStatus) int {
	switch a {
	case AggPending, AggAwaitingImport:
		return 1
	case AggImporting:
		return 2
	case AggImported:
		return 3
	case AggDeploying, AggRollbacking: // treat rollbacking as deploying for legacy clients
		return 4
	case AggDeployed, AggRolledBack:
		return 5
	case AggImportFailed:
		return 6 // new code (was not in original list)
	case AggDeployFailed:
		return 7
	case AggCanceled:
		return 9
	default:
		return 0
	}
}

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
