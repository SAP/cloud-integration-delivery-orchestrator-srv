package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v89/github"
)

// =============================================================================
// GitHub App exit mechanism — installation uninstall (DM-8 / §10)
//
// This is the API-able half of App teardown: DELETE /app/installations/{id}
// revokes the installation's access to its repos. It authenticates with an
// App-level JWT (NewAppsTransport), NOT an installation token — uninstalling is
// an App-owner operation. The App private key (base64 PEM) and target host come
// from the same auto-created destination used for sync, so no extra config is
// needed. Deleting the App *registration* itself remains UI-only (see
// AppAdvancedURL); this only detaches the installation.
// =============================================================================

// UninstallApp removes the App installation via DELETE /app/installations/{installationID},
// authenticated with an App-level JWT built from the destination's base64 PEM private key.
// destName resolves to the App's private key + GitHub host (public vs GHES).
func UninstallApp(ctx context.Context, destName string, appID, installationID int64, resolver destinationResolver) error {
	dest, err := resolver.GetDestination(ctx, destName)
	if err != nil {
		return fmt.Errorf("github destination %s not found: %w", destName, err)
	}
	if dest == nil {
		return fmt.Errorf("github destination %s not found", destName)
	}
	if dest.Password == "" {
		return fmt.Errorf("github destination %s has no password (base64 app private key)", destName)
	}
	keyPEM, err := base64.StdEncoding.DecodeString(dest.Password)
	if err != nil {
		return fmt.Errorf("decode github app private key from destination %s: %w", destName, err)
	}

	isGHES, apiBaseURL, uploadURL := resolveGitHubBaseURLs(dest.URL)

	// App-JWT transport: signs a ≤10min JWT as the App itself (no installation token exchange).
	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, keyPEM)
	if err != nil {
		return fmt.Errorf("create github app-jwt transport: %w", err)
	}
	if isGHES {
		atr.BaseURL = apiBaseURL
	}

	opts := []github.ClientOptionsFunc{github.WithTransport(atr)}
	if isGHES {
		opts = append(opts, github.WithEnterpriseURLs(apiBaseURL, uploadURL))
	}
	client, err := github.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("create github app client: %w", err)
	}

	if _, err := client.Apps.DeleteInstallation(ctx, installationID); err != nil {
		return fmt.Errorf("delete installation %d: %w", installationID, err)
	}
	return nil
}
