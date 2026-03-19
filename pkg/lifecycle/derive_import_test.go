package lifecycle

import "testing"

func TestDeriveImport_TMSStatuses(t *testing.T) {
	tests := []struct {
		raw  string
		want ImportState
	}{
		{"INITIAL", ImportQueued},
		{"initial", ImportQueued},
		{"RUNNING", ImportInProgress},
		{"SUCCEEDED", ImportComplete},
		{"WARNING", ImportComplete},
		{"warning", ImportComplete},
		{"REPEAT", ImportQueued},
		{"repeat", ImportQueued},
		{"FATAL", ImportFailed},
		{"FAILED", ImportFailed},
		{"ERROR", ImportFailed},
		{"UNKNOWN_TMS", ImportNotStarted},
	}
	for _, tt := range tests {
		if got := DeriveImport(tt.raw); got != tt.want {
			t.Errorf("DeriveImport(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
