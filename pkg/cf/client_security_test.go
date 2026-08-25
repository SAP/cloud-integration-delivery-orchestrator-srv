package cf

import (
	"context"
	"testing"
)

// =============================================================================
// ExchangeCfPasscode URL Validation Tests (SSRF Protection)
// =============================================================================

func TestExchangeCfPasscode_ValidEndpoints(t *testing.T) {
	// These should pass URL validation but fail on network (no real server).
	// We verify they don't return the "invalid API endpoint" error.
	valid := []string{
		"https://api.cf.eu10.hana.ondemand.com",
		"https://api.cf.us10.hana.ondemand.com",
		"https://api.cf.jp20.hana.ondemand.com",
		"https://api.cf.eu10-004.hana.ondemand.com",
	}
	for _, endpoint := range valid {
		_, err := ExchangeCfPasscode(context.Background(), endpoint, "dummy-passcode")
		if err == nil {
			continue // unexpected success (would need real server), but validation passed
		}
		// Should NOT be the SSRF validation error
		if containsString(err.Error(), "invalid API endpoint") {
			t.Errorf("ExchangeCfPasscode(%q) rejected as invalid endpoint, but should be valid", endpoint)
		}
	}
}

func TestExchangeCfPasscode_InvalidEndpoints(t *testing.T) {
	invalid := []struct {
		endpoint string
		desc     string
	}{
		{"https://api.evil.com", "non-SAP domain"},
		{"https://api.evil.hana.ondemand.com.attacker.com", "suffix bypass attempt"},
		{"http://api.cf.eu10.hana.ondemand.com", "HTTP instead of HTTPS"},
		{"https://internal-service.corp:8080", "internal service"},
		{"https://169.254.169.254", "cloud metadata IP"},
		{"ftp://api.cf.eu10.hana.ondemand.com", "wrong scheme"},
		{"", "empty string"},
		{"not-a-url", "malformed URL"},
	}
	for _, tc := range invalid {
		_, err := ExchangeCfPasscode(context.Background(), tc.endpoint, "dummy-passcode")
		if err == nil {
			t.Errorf("ExchangeCfPasscode(%q) [%s] should return error, got nil", tc.endpoint, tc.desc)
			continue
		}
		if !containsString(err.Error(), "invalid API endpoint") {
			t.Errorf("ExchangeCfPasscode(%q) [%s] error should mention 'invalid API endpoint', got: %s", tc.endpoint, tc.desc, err.Error())
		}
	}
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
