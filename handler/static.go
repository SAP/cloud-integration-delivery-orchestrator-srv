package handler

import (
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/auth"
)

// SetupStaticRoutes registers the embedded frontend as a NoRoute fallback.
// It serves files from the embedded FS, falling back to index.html for SPA routing.
// Like SAP Approuter, it requires a valid session before serving any content —
// unauthenticated users are redirected to the login page (top-level navigation).
func SetupStaticRoutes(router *gin.Engine, embedded fs.FS, sessions *auth.SessionStore, loginPath string) {
	distFS, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("failed to sub embedded FS: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(distFS))

	router.NoRoute(func(c *gin.Context) {
		// Check session before serving static content (same as Approuter behavior).
		// This ensures unauthenticated users get a top-level redirect to XSUAA,
		// rather than loading the SPA which then fails AJAX calls.
		cookie, err := c.Cookie(sessions.CookieName())
		if err != nil || cookie == "" {
			redirectToLogin(c, loginPath)
			return
		}
		if _, ok := sessions.Get(cookie); !ok {
			redirectToLogin(c, loginPath)
			return
		}

		// Authenticated — serve the file
		path := c.Request.URL.Path

		f, err := distFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		// File not found — serve index.html (SPA fallback)
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

func redirectToLogin(c *gin.Context, loginPath string) {
	path := c.Request.URL.Path
	// // Ignore non-user paths (browser/devtools probes) — redirect to root instead
	// if strings.HasPrefix(path, "/.well-known") || strings.HasPrefix(path, "/favicon") {
	// 	path = "/"
	// }
	redirect := loginPath + "?redirect=" + url.QueryEscape(path)
	c.Redirect(302, redirect)
}
