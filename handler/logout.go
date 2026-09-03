package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/auth"
)

// HandleLogout clears the local session and redirects to XSUAA logout.do
// to also clear the XSUAA SSO session. Without this, XSUAA would auto-login
// the user again immediately.
// GET /logout
func HandleLogout(sessions *auth.SessionStore, logoutURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Delete local session
		cookie, err := c.Cookie(sessions.CookieName())
		if err == nil && cookie != "" {
			sessions.Delete(cookie)
		}
		// 2. Clear cookie
		sessions.ClearCookie(c)
		// 3. Derive app host from request
		scheme := c.GetHeader("X-Forwarded-Proto")
		if scheme == "" {
			scheme = "http"
		}
		appHost := scheme + "://" + c.Request.Host
		// 4. Redirect to XSUAA logout.do (clears SSO session)
		// Parameters NOT percent-encoded (SAP XSUAA quirk, same as authorize endpoint)
		c.Redirect(302, logoutURL+"?redirect="+appHost)
	}
}
