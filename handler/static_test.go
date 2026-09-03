package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/auth"
)

func TestSetupStaticRoutes_ServesFileWithSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	sessions := auth.NewSessionStore("sid", 1*time.Hour)
	cookie := sessions.Create(&auth.SessionData{AccessToken: "tok"})

	mockFS := fstest.MapFS{
		"dist/index.html":    {Data: []byte("<html>SPA</html>")},
		"dist/assets/app.js": {Data: []byte("console.log('app')")},
	}

	SetupStaticRoutes(router, mockFS, sessions, "/auth/login")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: cookie})
	router.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "console.log")
}

func TestSetupStaticRoutes_SPAFallbackWithSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	sessions := auth.NewSessionStore("sid", 1*time.Hour)
	cookie := sessions.Create(&auth.SessionData{AccessToken: "tok"})

	mockFS := fstest.MapFS{
		"dist/index.html": {Data: []byte("<html>SPA</html>")},
	}

	SetupStaticRoutes(router, mockFS, sessions, "/auth/login")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: cookie})
	router.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), "SPA")
}

func TestSetupStaticRoutes_RedirectsWithoutSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	sessions := auth.NewSessionStore("sid", 1*time.Hour)

	mockFS := fstest.MapFS{
		"dist/index.html": {Data: []byte("<html>SPA</html>")},
	}

	SetupStaticRoutes(router, mockFS, sessions, "/auth/login")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, 302, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/auth/login")
	assert.Contains(t, w.Header().Get("Location"), "redirect=")
}

func TestSetupStaticRoutes_DoesNotConflictWithRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	sessions := auth.NewSessionStore("sid", 1*time.Hour)

	router.GET("/api/v1/test", func(c *gin.Context) {
		c.String(200, "api-response")
	})

	mockFS := fstest.MapFS{
		"dist/index.html": {Data: []byte("<html>SPA</html>")},
	}

	SetupStaticRoutes(router, mockFS, sessions, "/auth/login")

	// API route should still work (handled by its own middleware, not NoRoute)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "api-response", w.Body.String())
}
