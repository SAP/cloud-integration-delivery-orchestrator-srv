package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"
	gh "mmt-delivery/pkg/github"

	"github.com/gin-gonic/gin"
)

// =============================================================================
// GitHub App Manifest flow (RFC 010 doc 12 §9 / §9.1)
//
// Three browser-facing endpoints under the system group:
//   1. StartGitAppManifest   — returns the manifest + POST target + CSRF state.
//   2. GitAppManifestCallback — GitHub's redirect_url: code → App credentials →
//      auto-named BasicAuthentication destination (base64 PEM) + config, then
//      302 to the install deep link.
//   3. GitAppSetupCallback    — GitHub's setup_url: installation_id → config.
//
// Callbacks 2 & 3 are cross-site top-level GET navigations from GitHub; the
// SameSite=Lax session cookie is carried, so they sit safely under the system
// group's auth+scope (RP-1). They return 302 redirects to the SPA, never JSON
// (RP-3).
// =============================================================================

const (
	gitAppCallbackPath = "/api/v1/system/gitApp/callback"
	gitAppSetupPath    = "/api/v1/system/gitApp/setup"
)

// cfAppBaseURL returns the authoritative external base URL for building GitHub
// redirect/setup callbacks. On CF this is the platform-injected route
// (ApplicationURIs[0]) — non-spoofable, unlike the request Host (DM-3). Request
// host is a local-dev fallback only.
//
// APP_BASE_URL overrides everything and exists solely for local debugging: the
// manifest flow needs GitHub to redirect the browser back to the running server,
// but `sync-env` copies the deployed app's VCAP_APPLICATION verbatim, so
// ApplicationURIs[0] locally points at the remote CF route. Setting
// APP_BASE_URL=http://localhost:8080 in .env makes the callbacks reach the local
// server. It is never set in production, so the platform-injected source (DM-3)
// remains authoritative there.
func (h *Handler) cfAppBaseURL(c *gin.Context) string {
	if v := os.Getenv("APP_BASE_URL"); v != "" {
		return v
	}
	if app := env.AppEnv(); app != nil && len(app.ApplicationURIs) > 0 {
		return "https://" + app.ApplicationURIs[0]
	}
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}

// redirectSPA sends the browser back to the SPA with a status query param the
// frontend reads to show a toast (RP-3). Relative redirect keeps the same host.
func redirectSPA(c *gin.Context, status, reason string) {
	target := "/?gitApp=" + status
	if reason != "" {
		target += "&reason=" + url.QueryEscape(reason)
	}
	c.Redirect(http.StatusFound, target)
}

// StartGitAppManifest builds the App manifest and returns it with the POST target
// and a CSRF state token. The frontend auto-submits a form (hidden `manifest`
// field) to postUrl; GitHub redirects back to the callback with a one-time code.
//
// The App name is freshly randomised per run (DM-6): the manifest flow mints a
// new App every run, and a random name is globally unique in one shot without
// any collision-retry dance or user renaming.
//
// accountType (DM-7 / §9.0) selects App ownership: "org" (with a non-empty org
// query) points the POST at /organizations/<org>/settings/apps/new; anything else
// defaults to the personal /settings/apps/new. Because the manifest App is private,
// ownership must match the sync-target account.
//
// GET /api/v1/system/gitApp/manifest?githubUrl=<host>&accountType=<user|org>&org=<org>
func (h *Handler) StartGitAppManifest(c *gin.Context) {
	githubURL := c.Query("githubUrl") // empty → public github.com

	// org ownership only when accountType=org AND an org name is given (DM-7).
	org := ""
	if c.Query("accountType") == "org" {
		org = c.Query("org")
		if org == "" {
			Fail(c, http.StatusBadRequest, "org is required when accountType=org")
			return
		}
	}

	base := h.cfAppBaseURL(c)

	appName, err := gh.GenerateAppName()
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	manifest := gh.BuildManifest(appName, base, base+gitAppCallbackPath, base+gitAppSetupPath)
	manifestJSON, err := manifest.JSON()
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	// State carries the target host so the callback can reach the right GitHub API.
	state, err := h.gitAppState.Issue(githubURL)
	if err != nil {
		Fail(c, http.StatusInternalServerError, fmt.Sprintf("failed to issue state: %s", err))
		return
	}

	OK(c, gin.H{
		"postUrl":  gh.NewAppURL(githubURL, org) + "?state=" + url.QueryEscape(state),
		"manifest": manifestJSON,
		"state":    state,
	})
}

// GitAppManifestCallback handles GitHub's redirect_url: exchanges the one-time
// code for App credentials, writes the private key to an auto-named destination
// and the non-secret metadata to GitRepoConfig, then 302s to the install page.
//
// GET /api/v1/system/gitApp/callback?code=<code>&state=<state>
func (h *Handler) GitAppManifestCallback(c *gin.Context) {
	code := c.Query("code")
	githubURL, ok := h.gitAppState.Consume(c.Query("state"))
	if !ok {
		h.logger.Warnf("git app callback: invalid or expired state")
		redirectSPA(c, "err", "invalid_state")
		return
	}
	if code == "" {
		redirectSPA(c, "err", "missing_code")
		return
	}

	creds, err := gh.CompleteAppManifest(c.Request.Context(), githubURL, code)
	if err != nil {
		h.logger.Errorf("git app callback: complete manifest failed: %s", err)
		// A name clash is not expected given the random name, but if it ever
		// happens it is recoverable: the SPA simply restarts the flow, which
		// mints a fresh random name (RP-5). Anything else is generic.
		if gh.IsNameTaken(err) {
			redirectSPA(c, "err", "name_taken")
			return
		}
		redirectSPA(c, "err", "manifest_conversion_failed")
		return
	}

	// Auto-named BasicAuthentication destination stores base64(PEM) in Password (DM-4 / §5.1).
	destName := "github-app-" + creds.Slug
	destURL := githubURL
	if destURL == "" {
		destURL = "https://github.com"
	}
	dest := cf.Destination{
		Name:           destName,
		Description:    "DO NOT MODIFY. GitHub App private key for CPI Delivery git sync. Auto-created by manifest flow.",
		Type:           "HTTP",
		URL:            destURL,
		Authentication: "BasicAuthentication",
		ProxyType:      "Internet",
		User:           fmt.Sprintf("github-app:%d", creds.AppID),
		Password:       base64.StdEncoding.EncodeToString([]byte(creds.PEM)),
	}
	if err := h.destSvc.UpsertDestination(c.Request.Context(), dest); err != nil {
		h.logger.Errorf("git app callback: upsert destination failed: %s", err)
		redirectSPA(c, "err", "destination_write_failed")
		return
	}

	// Persist non-secret App metadata into the single GitRepoConfig row
	// (first-or-create, mirroring UpsertGitRepoConfig). InstallationID is filled
	// later by the setup callback; Repo/Enabled remain for the user/discovery step.
	var existing db.GitRepoConfig
	if err := h.db.First(&existing).Error; err != nil {
		newCfg := db.GitRepoConfig{
			Provider:        string(gh.ProviderGitHub),
			AuthMethod:      string(gh.AuthMethodGitHubApp),
			DestinationName: destName,
			GithubAppID:     creds.AppID,
			Owner:           creds.Owner,
		}
		if err := h.db.Create(&newCfg).Error; err != nil {
			h.logger.Errorf("git app callback: create config failed: %s", err)
			redirectSPA(c, "err", "config_write_failed")
			return
		}
	} else {
		existing.Provider = string(gh.ProviderGitHub)
		existing.AuthMethod = string(gh.AuthMethodGitHubApp)
		existing.DestinationName = destName
		existing.GithubAppID = creds.AppID
		existing.Owner = creds.Owner
		if err := h.db.Save(&existing).Error; err != nil {
			h.logger.Errorf("git app callback: save config failed: %s", err)
			redirectSPA(c, "err", "config_write_failed")
			return
		}
	}

	// One-click chain: send the admin straight to the install page. A fresh state
	// (carrying the same host) guards the setup_url callback.
	installState, err := h.gitAppState.Issue(githubURL)
	if err != nil {
		h.logger.Errorf("git app callback: issue install state failed: %s", err)
		redirectSPA(c, "err", "state_issue_failed")
		return
	}
	c.Redirect(http.StatusFound, gh.InstallURL(githubURL, creds.Slug, installState))
}

// GitAppSetupCallback handles GitHub's setup_url after installation: records the
// installation_id on the config.
//
// GET /api/v1/system/gitApp/setup?installation_id=<id>&state=<state>&setup_action=<action>
func (h *Handler) GitAppSetupCallback(c *gin.Context) {
	if _, ok := h.gitAppState.Consume(c.Query("state")); !ok {
		h.logger.Warnf("git app setup: invalid or expired state")
		redirectSPA(c, "err", "invalid_state")
		return
	}
	instID, err := strconv.ParseInt(c.Query("installation_id"), 10, 64)
	if err != nil || instID == 0 {
		redirectSPA(c, "err", "missing_installation_id")
		return
	}

	var cfg db.GitRepoConfig
	if err := h.db.First(&cfg).Error; err != nil {
		h.logger.Errorf("git app setup: no config to attach installation to: %s", err)
		redirectSPA(c, "err", "no_config")
		return
	}
	cfg.GithubInstallationID = instID
	if err := h.db.Save(&cfg).Error; err != nil {
		h.logger.Errorf("git app setup: save installation id failed: %s", err)
		redirectSPA(c, "err", "config_write_failed")
		return
	}
	redirectSPA(c, "ok", "")
}
