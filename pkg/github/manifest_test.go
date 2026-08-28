package github

import (
	"strings"
	"testing"
	"time"
)

func TestStateStore_IssueConsume(t *testing.T) {
	s := NewStateStore(5 * time.Minute)

	token, err := s.Issue("https://github.example.com")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("Issue returned empty token")
	}

	// First consume succeeds and returns the bound payload.
	payload, ok := s.Consume(token)
	if !ok {
		t.Fatal("Consume of valid token returned ok=false")
	}
	if payload != "https://github.example.com" {
		t.Fatalf("payload = %q, want the issued host", payload)
	}

	// Single-use: a second consume of the same token fails.
	if _, ok := s.Consume(token); ok {
		t.Fatal("second Consume of same token returned ok=true (must be single-use)")
	}
}

func TestStateStore_ConsumeInvalid(t *testing.T) {
	s := NewStateStore(5 * time.Minute)
	if _, ok := s.Consume(""); ok {
		t.Fatal("Consume(\"\") returned ok=true")
	}
	if _, ok := s.Consume("never-issued"); ok {
		t.Fatal("Consume of unknown token returned ok=true")
	}
}

func TestStateStore_Expired(t *testing.T) {
	// Negative TTL → token is born already expired.
	s := NewStateStore(-time.Second)
	token, err := s.Issue("payload")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, ok := s.Consume(token); ok {
		t.Fatal("Consume of expired token returned ok=true")
	}
}

func TestBuildManifest_NoWebhookMinimalPerms(t *testing.T) {
	m := BuildManifest("cpi-delivery-sync", "https://app.example.com",
		"https://app.example.com/api/v1/system/gitApp/callback",
		"https://app.example.com/api/v1/system/gitApp/setup")

	if m.Public {
		t.Error("manifest must not be public")
	}
	if got := m.DefaultPermissions["contents"]; got != "write" {
		t.Errorf("contents permission = %q, want write", got)
	}

	js, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	// hook_attributes must be absent entirely so the App has no webhook (§9).
	if strings.Contains(js, "hook_attributes") {
		t.Errorf("manifest JSON must not contain hook_attributes: %s", js)
	}
	if !strings.Contains(js, `"redirect_url":"https://app.example.com/api/v1/system/gitApp/callback"`) {
		t.Errorf("manifest JSON missing redirect_url: %s", js)
	}
}

func TestResolveGitHubWebBase(t *testing.T) {
	tests := []struct {
		name    string
		destURL string
		want    string
	}{
		{"empty → public", "", "https://github.com"},
		{"public github.com", "https://github.com", "https://github.com"},
		{"public api host", "https://api.github.com", "https://github.com"},
		{"GHES plain host", "https://github.example.com", "https://github.example.com"},
		{"GHES with junk path", "github.example.com/api/v3/", "https://github.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveGitHubWebBase(tt.destURL); got != tt.want {
				t.Errorf("resolveGitHubWebBase(%q) = %q, want %q", tt.destURL, got, tt.want)
			}
		})
	}
}

func TestNewAppURL(t *testing.T) {
	if got := NewAppURL(""); got != "https://github.com/settings/apps/new" {
		t.Errorf("public NewAppURL = %q", got)
	}
	if got := NewAppURL("https://github.example.com"); got != "https://github.example.com/settings/apps/new" {
		t.Errorf("GHES NewAppURL = %q", got)
	}
}

func TestInstallURL(t *testing.T) {
	got := InstallURL("", "my-app", "st4te")
	want := "https://github.com/apps/my-app/installations/new?state=st4te"
	if got != want {
		t.Errorf("InstallURL = %q, want %q", got, want)
	}
	// No state → no query string.
	if got := InstallURL("", "my-app", ""); got != "https://github.com/apps/my-app/installations/new" {
		t.Errorf("InstallURL without state = %q", got)
	}
}

func TestGenerateAppName(t *testing.T) {
	a, err := GenerateAppName()
	if err != nil {
		t.Fatalf("GenerateAppName: %v", err)
	}
	b, err := GenerateAppName()
	if err != nil {
		t.Fatalf("GenerateAppName: %v", err)
	}
	// Fresh per call so each manifest run mints a unique App name (never self-collides).
	if a == b {
		t.Errorf("GenerateAppName not unique across calls: %q == %q", a, b)
	}
	if !strings.HasPrefix(a, appNamePrefix) {
		t.Errorf("name %q missing prefix %q", a, appNamePrefix)
	}
	// Must stay within GitHub's 34-char App-name limit.
	if len(a) > 34 {
		t.Errorf("name %q exceeds 34-char limit (%d)", a, len(a))
	}
}

func TestIsNameTaken(t *testing.T) {
	if !IsNameTaken(errTest("Name is already taken")) {
		t.Error("IsNameTaken should match GitHub's 'already taken' message")
	}
	if IsNameTaken(nil) {
		t.Error("IsNameTaken(nil) must be false")
	}
	if IsNameTaken(errTest("some unrelated failure")) {
		t.Error("IsNameTaken matched an unrelated error")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
