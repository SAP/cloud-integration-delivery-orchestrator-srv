// Package errcode defines machine-readable error codes shared between
// the backend HTTP API and the frontend error classifier (RFC 019).
//
// These codes appear in the JSON response body's "code" field and allow
// the frontend to distinguish error categories without parsing messages.
package errcode

const (
	InvalidInput        = "INVALID_INPUT"
	Unauthorized        = "UNAUTHORIZED"
	Forbidden           = "FORBIDDEN"
	NotFound            = "NOT_FOUND"
	Conflict            = "CONFLICT"
	UpstreamUnavailable = "GATEWAY_UNAVAILABLE"
	TooManyRequests     = "TOO_MANY_REQUESTS"
	Internal            = "BACKEND_ERROR"

	// SnapshotOrphaned marks a completed snapshot whose pinned commit is no
	// longer resolvable in the currently-configured repo (e.g. after a repo
	// target switch). The frontend uses this to offer a Re-sync recovery
	// path instead of a dead error banner (RFC 010 · 13).
	SnapshotOrphaned = "SNAPSHOT_ORPHANED"
)
