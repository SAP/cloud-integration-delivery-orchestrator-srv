package lifecycle

import "strings"

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
