package github

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v89/github"
)

// =============================================================================
// GitHub App Manifest flow (RFC 010 doc 12 §9 / §9.1)
//
// This file implements the App-creation side of the dual-mode auth: building the
// App manifest, guarding the browser round-trip with a CSRF state token, and
// exchanging the one-time code for the created App's credentials. It does NOT do
// installation-token auth — that lives in github_client.go via ghinstallation.
// =============================================================================

// AppManifest is the GitHub App manifest posted to /settings/apps/new.
// hook_attributes is deliberately omitted so the created App has no webhook
// (DM-1 / §9): this integration only pushes, never consumes webhooks. Providing
// hook_attributes would force a required `url` field, so we leave it out entirely.
type AppManifest struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	URL                string            `json:"url"`          // App homepage (required by GitHub)
	RedirectURL        string            `json:"redirect_url"` // where the one-time code lands
	SetupURL           string            `json:"setup_url,omitempty"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

// BuildManifest constructs a minimal-permission (Contents: R/W), webhook-less
// App manifest. name must be globally unique on the target GitHub host.
// description carries deployment context (Markdown) so the App is identifiable
// in GitHub settings; it is purely informational and has no length constraint.
func BuildManifest(name, description, appHomeURL, redirectURL, setupURL string) AppManifest {
	return AppManifest{
		Name:               name,
		Description:        description,
		URL:                appHomeURL,
		RedirectURL:        redirectURL,
		SetupURL:           setupURL,
		Public:             false,
		DefaultPermissions: map[string]string{"contents": "write"},
	}
}

// =============================================================================
// Deployment-unique App naming (DM-6 / §9.2)
//
// GitHub App names must be globally unique on the target host — a duplicate name
// hard-fails at manifest conversion (GitHub does NOT auto-suffix) and, per
// synlynk#901, is reserved the moment the manifest is POSTed even if the flow
// aborts. The manifest flow's own semantics are "every run mints a brand-new
// App", so we mint a fresh random name per run rather than a fixed or
// deployment-derived one: this guarantees uniqueness in a single shot (no
// retry/collision dance) and never self-collides with a name a prior aborted
// run left reserved.
// =============================================================================

const appNamePrefix = "delivery-orch-" // 14 chars; leaves ample room under the 34-char limit

// GenerateAppName returns a fresh, globally-unique-enough GitHub App name:
// "delivery-orch-<16hex>" (30 chars, 64 bits of entropy — collision on the
// global namespace is negligible). Called once per manifest run.
func GenerateAppName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate app name: %w", err)
	}
	return appNamePrefix + hex.EncodeToString(b), nil
}

// IsNameTaken reports whether err is GitHub's "name already taken" manifest
// conversion failure. Kept as a defensive classifier so the caller can surface a
// clear reason; with GenerateAppName's entropy this is not expected to fire.
func IsNameTaken(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already")
}

// JSON serializes the manifest for the hidden `manifest` form field the browser
// POSTs to GitHub.
func (m AppManifest) JSON() (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal app manifest: %w", err)
	}
	return string(b), nil
}

// =============================================================================
// CSRF state store (DM-2 / RP-2)
//
// In-process, TTL-bounded random tokens guarding the browser round-trip. This is
// architecturally consistent with the existing in-process SessionStore (both
// rely on single-instance / CF sticky routing). The token is anti-forgery only —
// it is NOT a GitHub credential.
// =============================================================================

// stateEntry is one issued token: an opaque payload plus its expiry. The payload
// carries flow context (the target GitHub host) so the callback stays stateless
// w.r.t. request params.
type stateEntry struct {
	payload string
	expires time.Time
}

// StateStore issues and consumes single-use, TTL-bounded CSRF state tokens, each
// binding an opaque payload set at issue time.
type StateStore struct {
	mu  sync.Mutex
	m   map[string]stateEntry
	ttl time.Duration
}

// DefaultStateTTL is the recommended lifetime for manifest-flow CSRF state
// tokens: generous enough to cover the admin's repo-selection step on GitHub's
// install page, short enough to bound the anti-forgery window. The composition
// root passes this to NewStateStore.
const DefaultStateTTL = 15 * time.Minute

// NewStateStore creates a StateStore whose tokens expire after ttl.
func NewStateStore(ttl time.Duration) *StateStore {
	return &StateStore{m: make(map[string]stateEntry), ttl: ttl}
}

// Issue returns a fresh random token bound to payload, valid for the store's TTL.
func (s *StateStore) Issue(payload string) (string, error) {
	// 16 bytes (128 bits) hex-encoded → 32 chars: ample entropy for a single-use,
	// 15-min anti-forgery token (it is not a long-lived credential) while keeping
	// the ?state= on the manifest POST URL short.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state token: %w", err)
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.gcLocked()
	s.m[token] = stateEntry{payload: payload, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return token, nil
}

// Consume validates and removes a token in one step, returning its payload.
// ok is true only if the token was present and unexpired. Single-use: a second
// Consume of the same token always returns ("", false).
func (s *StateStore) Consume(token string) (payload string, ok bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, present := s.m[token]
	if !present {
		return "", false
	}
	delete(s.m, token)
	if time.Now().After(e.expires) {
		return "", false
	}
	return e.payload, true
}

// gcLocked drops expired tokens. Caller must hold s.mu.
func (s *StateStore) gcLocked() {
	now := time.Now()
	for k, e := range s.m {
		if now.After(e.expires) {
			delete(s.m, k)
		}
	}
}

// =============================================================================
// Browser-facing GitHub URLs (RP-4)
// =============================================================================

// resolveGitHubWebBase returns the web base ("https://<host>") for browser-facing
// GitHub App pages. Public github.com → "https://github.com"; GHES → its host.
// Reuses resolveGitHubBaseURLs so host classification stays in one place.
func resolveGitHubWebBase(destURL string) string {
	isGHES, apiBaseURL, _ := resolveGitHubBaseURLs(destURL)
	if !isGHES {
		return "https://github.com"
	}
	return strings.TrimSuffix(apiBaseURL, "/api/v3")
}

// NewAppURL is the manifest form target. The org argument (DM-7 / §9.0) selects
// ownership of the created App: empty → personal account
// (<web-base>/settings/apps/new); non-empty → that org
// (<web-base>/organizations/<org>/settings/apps/new). Because the manifest App is
// private (public:false), it can only be installed on the account that owns it,
// so ownership must match the sync-target account. The browser POSTs the manifest
// (with ?state=<csrf>) here.
func NewAppURL(destURL, org string) string {
	base := resolveGitHubWebBase(destURL)
	if org == "" {
		return base + "/settings/apps/new"
	}
	return base + "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
}

// InstallURL is the post-creation install deep link. The manifest App is private
// (public:false), so the public listing page (<web-base>/apps/<slug>/…) does not
// exist and 404s; a private App is installed from its owner's App-settings page
// instead. ownerType (from the conversion response's owner.type) selects the path:
// "Organization" → <web-base>/organizations/<owner>/settings/apps/<slug>/installations;
// anything else (personal) → <web-base>/settings/apps/<slug>/installations.
//
// No ?state= is appended: GitHub preserves the install-URL state only for the
// public /apps/<slug>/installations/new flow, not the settings install page, so
// the setup_url callback cannot rely on it (see handler.GitAppSetupCallback).
func InstallURL(destURL, ownerType, owner, slug string) string {
	base := resolveGitHubWebBase(destURL)
	if strings.EqualFold(ownerType, "Organization") {
		return base + "/organizations/" + url.PathEscape(owner) + "/settings/apps/" + url.PathEscape(slug) + "/installations"
	}
	return base + "/settings/apps/" + url.PathEscape(slug) + "/installations"
}

// AppSettingsURL is the created App's general settings page, where the admin can
// view the App name, description, permissions, and navigate to installations or
// the advanced (delete) page. ownerType selects the path like InstallURL.
func AppSettingsURL(destURL, ownerType, owner, slug string) string {
	base := resolveGitHubWebBase(destURL)
	if strings.EqualFold(ownerType, "Organization") {
		return base + "/organizations/" + url.PathEscape(owner) + "/settings/apps/" + url.PathEscape(slug)
	}
	return base + "/settings/apps/" + url.PathEscape(slug)
}

// AppAdvancedURL is the created App's "Advanced" settings page, where the admin
// finishes the exit flow by clicking "Delete GitHub App" (DM-8 / §10). Deleting
// the App *registration* is UI-only — GitHub exposes no REST API for it (unlike
// uninstalling an installation, which UninstallApp does). ownerType selects the
// path exactly like InstallURL: "Organization" →
// <web-base>/organizations/<owner>/settings/apps/<slug>/advanced; anything else
// (personal) → <web-base>/settings/apps/<slug>/advanced.
func AppAdvancedURL(destURL, ownerType, owner, slug string) string {
	base := resolveGitHubWebBase(destURL)
	if strings.EqualFold(ownerType, "Organization") {
		return base + "/organizations/" + url.PathEscape(owner) + "/settings/apps/" + url.PathEscape(slug) + "/advanced"
	}
	return base + "/settings/apps/" + url.PathEscape(slug) + "/advanced"
}

// =============================================================================
// Manifest → App credentials (DM-1)
// =============================================================================

// AppCredentials is the subset of the manifest-conversion result we persist.
// ClientID/ClientSecret/WebhookSecret from the conversion are intentionally
// dropped (§9: unused, never persisted).
type AppCredentials struct {
	AppID     int64
	Slug      string
	Owner     string
	OwnerType string // "User" or "Organization" — selects the install-page URL shape
	PEM       string // raw PEM private key (caller base64-encodes before storing)
}

// CompleteAppManifest exchanges a one-time manifest code for the created App's
// credentials via GitHub's conversions endpoint (POST /app-manifests/{code}/conversions).
// Uses an unauthenticated client — the code itself is the credential. destURL
// selects public github.com vs GHES.
func CompleteAppManifest(ctx context.Context, destURL, code string) (*AppCredentials, error) {
	client, err := newUnauthenticatedClient(destURL)
	if err != nil {
		return nil, err
	}
	cfg, _, err := client.Apps.CompleteAppManifest(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("complete app manifest: %w", err)
	}
	if cfg.GetID() == 0 || cfg.GetPEM() == "" {
		return nil, fmt.Errorf("manifest conversion returned incomplete app config (missing id or pem)")
	}
	return &AppCredentials{
		AppID:     cfg.GetID(),
		Slug:      cfg.GetSlug(),
		Owner:     cfg.GetOwner().GetLogin(),
		OwnerType: cfg.GetOwner().GetType(),
		PEM:       cfg.GetPEM(),
	}, nil
}

// newUnauthenticatedClient builds a go-github client with no auth, pointed at the
// right host. GHES reuses the already-resolved /api/v3 + /api/uploads URLs.
func newUnauthenticatedClient(destURL string) (*github.Client, error) {
	isGHES, apiBaseURL, uploadURL := resolveGitHubBaseURLs(destURL)
	if !isGHES {
		client, err := github.NewClient()
		if err != nil {
			return nil, fmt.Errorf("create github client: %w", err)
		}
		return client, nil
	}
	client, err := github.NewClient(github.WithEnterpriseURLs(apiBaseURL, uploadURL))
	if err != nil {
		return nil, fmt.Errorf("create github enterprise client: %w", err)
	}
	return client, nil
}
