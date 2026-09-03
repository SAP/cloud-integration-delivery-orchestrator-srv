package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/auth"
)

func TestHandleLogout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sessions := auth.NewSessionStore("test_sid", 1*time.Hour)
	cookie := sessions.Create(&auth.SessionData{AccessToken: "token123"})

	h := HandleLogout(sessions, "https://auth.example.com/logout.do")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/logout", nil)
	c.Request.Host = "myapp.example.com"
	c.Request.AddCookie(&http.Cookie{Name: "test_sid", Value: cookie})

	h(c)

	// Verify redirect to XSUAA logout.do
	assert.Equal(t, 302, w.Code)
	location := w.Header().Get("Location")
	assert.Contains(t, location, "https://auth.example.com/logout.do")
	assert.Contains(t, location, "redirect=http://myapp.example.com")

	// Verify session is deleted
	_, ok := sessions.Get(cookie)
	assert.False(t, ok)
}

func TestHandleLogout_NoSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sessions := auth.NewSessionStore("test_sid", 1*time.Hour)
	h := HandleLogout(sessions, "https://auth.example.com/logout.do")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/logout", nil)
	c.Request.Host = "myapp.example.com"

	h(c)

	assert.Equal(t, 302, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "logout.do")
}
