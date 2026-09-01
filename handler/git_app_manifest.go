package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

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

// gitAppRedirectReason is an internal key selecting the human-readable message
// the manifest callbacks send on failure. Per the redirect contract (RFC 010
// doc 12 RP-3) the backend authors the toast text — mirroring OKMsg()/Fail() —
// so the reason code never crosses to the client; only its message does. The
// typed const keeps call sites self-documenting and the message table in one
// place.
type gitAppRedirectReason string

const (
	reasonInvalidState             gitAppRedirectReason = "invalid_state"
	reasonMissingCode              gitAppRedirectReason = "missing_code"
	reasonNameTaken                gitAppRedirectReason = "name_taken"
	reasonManifestConversionFailed gitAppRedirectReason = "manifest_conversion_failed"
	reasonDestinationWriteFailed   gitAppRedirectReason = "destination_write_failed"
	reasonConfigWriteFailed        gitAppRedirectReason = "config_write_failed"
	reasonMissingInstallationID    gitAppRedirectReason = "missing_installation_id"
	reasonNoConfig                 gitAppRedirectReason = "no_config"
)

// gitAppReasonMessages is the single source of truth for the failure toast text
// (moved here from the frontend so it matches the OKMsg/Fail convention where the
// backend authors messages). name_taken nudges the user to re-run the flow, which
// mints a fresh random name (RP-5).
var gitAppReasonMessages = map[gitAppRedirectReason]string{
	reasonInvalidState:             "The registration link expired or was invalid. Please try again.",
	reasonMissingCode:              "GitHub did not return an authorization code. Please try again.",
	reasonNameTaken:                "The generated App name was already taken. Please retry — a new name will be used.",
	reasonManifestConversionFailed: "GitHub could not create the App from the manifest. Please try again.",
	reasonDestinationWriteFailed:   "Failed to store the App credentials. Please contact your administrator.",
	reasonConfigWriteFailed:        "Failed to save the GitHub App configuration. Please try again.",
	reasonMissingInstallationID:    "The installation did not complete. Please install the App on a repository.",
	reasonNoConfig:                 "No configuration found to attach the installation to. Please re-register the App.",
}

// gitAppLandingPath is where every manifest redirect (success or failure) lands:
// the System Config page, next to the Git dialog the admin started from. Success
// additionally carries openGitDialog=1 so SystemConfigView reopens the dialog on
// its own mount — no frontend interpretation of the outcome is needed.
const gitAppLandingPath = "/config/system-config"

// cfAppBaseURL returns the authoritative external base URL for building GitHub
// redirect/setup callbacks. On CF this is the platform-injected route
// (ApplicationURIs[0]) — non-spoofable, unlike the request Host (DM-3). Request
// host is a local-dev fallback only.
//
// APP_BASE_URL overrides everything and exists solely for local debugging: the
// manifest flow needs GitHub to redirect the browser back to the running server,
// cfAppBaseURL returns the externally-reachable base URL of this deployment.
// In production, CF injects ApplicationURIs via VCAP_APPLICATION and the first
// URI is used (with https). Locally, `sync-env` strips application_uris from
// the pulled VCAP_APPLICATION so this falls through to the request Host
// (http://localhost:8080), keeping callback URLs correct without a separate
// override env var.
func (h *Handler) cfAppBaseURL(c *gin.Context) string {
	if app := env.AppEnv(); app != nil && len(app.ApplicationURIs) > 0 {
		return "https://" + app.ApplicationURIs[0]
	}
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host
}

// buildAppDescription assembles a Markdown description for the GitHub App manifest
// so the App is identifiable in GitHub settings. Includes deployment context from
// VCAP_APPLICATION when available; gracefully degrades when fields are missing.
func (h *Handler) buildAppDescription(baseURL string) string {
	appName := "unknown"
	spaceName := ""
	if app := env.AppEnv(); app != nil {
		if app.Name != "" {
			appName = app.Name
		}
		spaceName = app.SpaceName
	}
	desc := fmt.Sprintf(
		"Auto-registered by **%s** to sync SAP Cloud Integration artifacts "+
			"to GitHub for version control and code comparison.\n\n"+
			"| Property | Value |\n|----------|-------|\n"+
			"| Application | %s |\n"+
			"| URL | %s |\n",
		appName, appName, baseURL,
	)
	if spaceName != "" {
		desc += fmt.Sprintf("| Space | %s |\n", spaceName)
	}
	desc += fmt.Sprintf("| Registered | %s |\n", time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	return desc
}

// gitAppFail 302s back to the System Config page with an error toast carrying the
// backend-authored message for reason. The frontend just shows it (RP-3).
func gitAppFail(c *gin.Context, reason gitAppRedirectReason) {
	RedirectSPA(c, gitAppLandingPath, "error", gitAppReasonMessages[reason], nil)
}

// gitAppOK 302s back to the System Config page with a success toast and the
// openGitDialog flag, so the dialog reopens on SystemConfigView's own mount.
func gitAppOK(c *gin.Context) {
	RedirectSPA(c, gitAppLandingPath, "success",
		"GitHub App registered and installed successfully.",
		url.Values{"openGitDialog": {"1"}})
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

	manifest := gh.BuildManifest(appName, h.buildAppDescription(base), base, base+gitAppCallbackPath, base+gitAppSetupPath)
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
		gitAppFail(c, reasonInvalidState)
		return
	}
	if code == "" {
		gitAppFail(c, reasonMissingCode)
		return
	}

	creds, err := gh.CompleteAppManifest(c.Request.Context(), githubURL, code)
	if err != nil {
		h.logger.Errorf("git app callback: complete manifest failed: %s", err)
		// A name clash is not expected given the random name, but if it ever
		// happens it is recoverable: the SPA simply restarts the flow, which
		// mints a fresh random name (RP-5). Anything else is generic.
		if gh.IsNameTaken(err) {
			gitAppFail(c, reasonNameTaken)
			return
		}
		gitAppFail(c, reasonManifestConversionFailed)
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
		gitAppFail(c, reasonDestinationWriteFailed)
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
			GithubOwnerType: creds.OwnerType,
			GithubAppSlug:   creds.Slug,
		}
		if err := h.db.Create(&newCfg).Error; err != nil {
			h.logger.Errorf("git app callback: create config failed: %s", err)
			gitAppFail(c, reasonConfigWriteFailed)
			return
		}
	} else {
		existing.Provider = string(gh.ProviderGitHub)
		existing.AuthMethod = string(gh.AuthMethodGitHubApp)
		existing.DestinationName = destName
		existing.GithubAppID = creds.AppID
		existing.Owner = creds.Owner
		existing.GithubOwnerType = creds.OwnerType
		existing.GithubAppSlug = creds.Slug
		if err := h.db.Save(&existing).Error; err != nil {
			h.logger.Errorf("git app callback: save config failed: %s", err)
			gitAppFail(c, reasonConfigWriteFailed)
			return
		}
	}

	// One-click chain: send the admin straight to the App's settings install page
	// (personal or org, per the conversion-reported owner type). A private App has
	// no public /apps/<slug> page, and GitHub does NOT preserve a ?state= across the
	// settings install page → setup_url, so the setup callback is guarded by the
	// system group's auth+scope rather than a CSRF token (see GitAppSetupCallback).
	c.Redirect(http.StatusFound, gh.InstallURL(githubURL, creds.OwnerType, creds.Owner, creds.Slug))
}

// GitAppSetupCallback handles GitHub's setup_url after installation: records the
// installation_id on the config.
//
// No CSRF state is validated here: the install step happens on the App's private
// settings page (a private App has no public /apps/<slug> page), and GitHub does
// NOT preserve a ?state= query across that page to the setup_url — documented only
// for the public install URL, and confirmed by community reports (github/community
// discussions #61291/#64239). The endpoint is instead guarded by the system group's
// auth+scope (RP-1): only an authenticated admin session reaches it, and it merely
// attaches installation_id to the single config row already created by the (state-
// validated) manifest callback.
//
// GET /api/v1/system/gitApp/setup?installation_id=<id>&setup_action=<action>
func (h *Handler) GitAppSetupCallback(c *gin.Context) {
	instID, err := strconv.ParseInt(c.Query("installation_id"), 10, 64)
	if err != nil || instID == 0 {
		gitAppFail(c, reasonMissingInstallationID)
		return
	}

	var cfg db.GitRepoConfig
	if err := h.db.First(&cfg).Error; err != nil {
		h.logger.Errorf("git app setup: no config to attach installation to: %s", err)
		gitAppFail(c, reasonNoConfig)
		return
	}
	cfg.GithubInstallationID = instID
	if err := h.db.Save(&cfg).Error; err != nil {
		h.logger.Errorf("git app setup: save installation id failed: %s", err)
		gitAppFail(c, reasonConfigWriteFailed)
		return
	}
	gitAppOK(c)
}

// GetGitAppInstallURL returns the install deep-link for an App that is registered but not yet
// installed (github_app mode, GithubInstallationID still 0). It is the same settings-install URL
// GitAppManifestCallback 302s to right after registration (gh.InstallURL) — rebuilt here so the SPA
// can surface a "Finish installing on GitHub" link when the admin returns to the install-pending
// dialog later instead of hunting for the App in GitHub's settings. Resolving the GitHub host (GHES
// vs github.com) needs the destination URL, which only the backend knows — hence a backend endpoint.
// The link works standalone: GitHub doesn't carry ?state= from the settings install page to the
// setup_url, and GitAppSetupCallback deliberately doesn't rely on it (guarded by the system group's
// auth+scope), so the deferred install still completes and back-fills GithubInstallationID.
//
// GET /api/v1/system/gitApp/installUrl
func (h *Handler) GetGitAppInstallURL(c *gin.Context) {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		Fail(c, http.StatusNotFound, "no GitRepoConfig found")
		return
	}
	if gh.AuthMethod(config.AuthMethod) != gh.AuthMethodGitHubApp {
		Fail(c, http.StatusBadRequest, "install URL is only valid in github_app mode")
		return
	}
	if config.GithubAppID == 0 {
		Fail(c, http.StatusConflict, "GitHub App not registered yet")
		return
	}
	if config.GithubInstallationID != 0 {
		Fail(c, http.StatusBadRequest, "GitHub App already installed")
		return
	}

	// Resolve the GitHub host from the destination (GHES vs public); fall back to public github.com.
	destURL := "https://github.com"
	if dest, err := h.destSvc.GetDestination(c.Request.Context(), config.DestinationName); err == nil && dest != nil && dest.URL != "" {
		destURL = dest.URL
	}

	OK(c, gin.H{"installUrl": gh.InstallURL(destURL, config.GithubOwnerType, config.Owner, config.GithubAppSlug)})
}

// GitAppDisconnect tears down the GitHub App integration (exit mechanism, RFC 010
// doc 12 §10). It performs a layered, best-effort cleanup and then hands the admin
// the deep-link needed to finish the one step that has no API:
//
//	① Uninstall the installation via App-JWT (DELETE /app/installations/{id}) — revokes
//	   the installation's repo access. Best-effort/logged: a failure here (already
//	   uninstalled, transient GitHub error) must not strand the admin, so we still tear
//	   down local state and hand them the Advanced-page link to clean up manually.
//	② Delete the auto-created destination holding the base64 PEM private key. Best-effort:
//	   the key is useless once ① revokes the installation, but we remove it so no stale
//	   secret lingers.
//	③ Delete the GitRepoConfig row (authoritative local unbind) — the one step that MUST
//	   succeed for the integration to be considered removed.
//
// Deleting the App *registration* itself is UI-only (GitHub exposes no REST API), so the
// response carries the App's Advanced-settings deep-link (personal vs org, per the
// persisted owner type) for the admin to click "Delete GitHub App".
//
// DELETE /api/v1/system/gitApp
func (h *Handler) GitAppDisconnect(c *gin.Context) {
	var config db.GitRepoConfig
	if err := h.db.First(&config).Error; err != nil {
		Fail(c, http.StatusNotFound, "no GitRepoConfig found")
		return
	}
	if gh.AuthMethod(config.AuthMethod) != gh.AuthMethodGitHubApp {
		Fail(c, http.StatusBadRequest, "git repo config is not in github_app mode")
		return
	}

	// slug is the App's human-readable URL name (persisted by the manifest callback), required
	// by GitHub's /settings/apps/<slug>/advanced page — distinct from the numeric GithubAppID.
	slug := config.GithubAppSlug

	// Resolve the destination host up front — we need it to build the Advanced-page
	// deep-link, and ② deletes the destination below. Fall back to public github.com.
	destURL := "https://github.com"
	if dest, err := h.destSvc.GetDestination(c.Request.Context(), config.DestinationName); err == nil && dest != nil && dest.URL != "" {
		destURL = dest.URL
	}
	advancedURL := gh.AppAdvancedURL(destURL, config.GithubOwnerType, config.Owner, slug)

	// ① Best-effort API uninstall (needs the destination's PEM, so before ②).
	if config.GithubInstallationID != 0 {
		if err := gh.UninstallApp(c.Request.Context(), config.DestinationName, config.GithubAppID, config.GithubInstallationID, h.destSvc); err != nil {
			h.logger.Warnf("git app disconnect: uninstall installation %d failed (continuing): %s", config.GithubInstallationID, err)
		}
	}

	// ② Best-effort destination delete (removes the stored private key).
	if err := h.destSvc.DeleteDestination(c.Request.Context(), config.DestinationName); err != nil {
		h.logger.Warnf("git app disconnect: delete destination %s failed (continuing): %s", config.DestinationName, err)
	}

	// ③ Authoritative local unbind. Hard delete so the single-row invariant holds and a
	// later re-registration starts clean (gorm.Model would otherwise soft-delete).
	if err := h.db.Unscoped().Delete(&config).Error; err != nil {
		h.logger.Errorf("git app disconnect: delete config failed: %s", err)
		Fail(c, http.StatusInternalServerError, "failed to remove git repo config")
		return
	}

	OK(c, gin.H{
		"advancedUrl": advancedURL,
		"message":     "GitHub App disconnected. To fully delete the App registration on GitHub, open its Advanced settings page and click \"Delete GitHub App\".",
	})
}
