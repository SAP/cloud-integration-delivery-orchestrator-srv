package service

import (
	"strings"
	"testing"
)

func TestTransportModuleURL_TrimsTrailingSlash(t *testing.T) {
	got := transportModuleURL("https://example.sap.com///")
	want := "https://example.sap.com/api/1.0/transportmodule/Transport"
	if got != want {
		t.Fatalf("transportModuleURL = %q, want %q", got, want)
	}
}

func TestBuildOAuthDestination_ResolvesCredentialShapes(t *testing.T) {
	cases := []struct {
		name         string
		urlTransform func(string) string
		creds        map[string]any
		wantURL      string
		wantToken    string
		wantID       string
		wantSecret   string
	}{
		{
			name:         "top level credentials with URL transform",
			urlTransform: transportModuleURL,
			creds: map[string]any{
				"url":          "https://pir.example.com",
				"clientid":     "pir-client",
				"clientsecret": "pir-secret",
				"tokenurl":     "https://auth.example.com",
			},
			wantURL:    "https://pir.example.com/api/1.0/transportmodule/Transport",
			wantToken:  "https://auth.example.com/oauth/token",
			wantID:     "pir-client",
			wantSecret: "pir-secret",
		},
		{
			name: "nested uaa credentials",
			creds: map[string]any{
				"url": "https://cas-standard.example.com",
				"uaa": map[string]any{
					"clientid":     "uaa-client",
					"clientsecret": "uaa-secret",
					"url":          "https://uaa.example.com",
				},
			},
			wantURL:    "https://cas-standard.example.com",
			wantToken:  "https://uaa.example.com/oauth/token",
			wantID:     "uaa-client",
			wantSecret: "uaa-secret",
		},
		{
			name: "nested oauth credentials",
			creds: map[string]any{
				"oauth": map[string]any{
					"url":          "https://pir-oauth.example.com",
					"tokenurl":     "https://oauth.example.com/oauth/token",
					"clientid":     "oauth-client",
					"clientsecret": "oauth-secret",
				},
			},
			wantURL:    "https://pir-oauth.example.com",
			wantToken:  "https://oauth.example.com/oauth/token",
			wantID:     "oauth-client",
			wantSecret: "oauth-secret",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest, err := buildOAuthDestination("CloudIntegration", tc.urlTransform, 42, tc.creds)
			if err != nil {
				t.Fatalf("buildOAuthDestination failed: %v", err)
			}
			if dest.URL != tc.wantURL {
				t.Fatalf("destination URL = %q, want %q", dest.URL, tc.wantURL)
			}
			if dest.TokenServiceURL != tc.wantToken {
				t.Fatalf("token URL = %q, want %q", dest.TokenServiceURL, tc.wantToken)
			}
			if dest.ClientId != tc.wantID || dest.ClientSecret != tc.wantSecret {
				t.Fatalf("unexpected oauth credentials: %+v", dest)
			}
			if dest.Authentication != "OAuth2ClientCredentials" || dest.ProxyType != "Internet" {
				t.Fatalf("unexpected destination defaults: %+v", dest)
			}
		})
	}
}

func TestBuildOAuthDestination_RejectsIncompleteCredentials(t *testing.T) {
	_, err := buildOAuthDestination("BrokenDestination", nil, 7, map[string]any{
		"url": "https://broken.example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "incomplete service key credentials") {
		t.Fatalf("expected incomplete credentials error, got %v", err)
	}
}

func TestGetTenantForBootstrap_ReturnsSeededTenantAndNotFound(t *testing.T) {
	tc := newTestCleanup(t)
	tenant := seedTenant(t, tc, "bootstrap-tenant-"+t.Name())

	svc := newTestService(nil)
	got, err := svc.getTenantForBootstrap(tenant.ID)
	if err != nil {
		t.Fatalf("getTenantForBootstrap failed: %v", err)
	}
	if got.ID != tenant.ID {
		t.Fatalf("tenant ID = %d, want %d", got.ID, tenant.ID)
	}

	if _, err := svc.getTenantForBootstrap(999999); err == nil || !strings.Contains(err.Error(), "tenant 999999 not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}
