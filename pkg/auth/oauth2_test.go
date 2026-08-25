package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── StateStore tests ──────────────────────────────────────────────────────────

func TestStateStore_GenerateAndValidate(t *testing.T) {
	s := NewStateStore()

	state := s.Generate("/dashboard")
	assert.NotEmpty(t, state)

	// First validation: must succeed and return the redirect URI.
	got, ok := s.Validate(state)
	require.True(t, ok)
	assert.Equal(t, "/dashboard", got)

	// Second validation: state was consumed, must fail.
	_, ok = s.Validate(state)
	assert.False(t, ok, "state should be single-use")
}

func TestStateStore_Expired(t *testing.T) {
	s := NewStateStore()

	state := s.Generate("/some-page")

	// Manually back-date the entry so it looks expired.
	s.mu.Lock()
	entry := s.states[state]
	entry.createdAt = time.Now().Add(-(stateTTL + time.Second))
	s.states[state] = entry
	s.mu.Unlock()

	_, ok := s.Validate(state)
	assert.False(t, ok, "expired state should be rejected")
}

func TestStateStore_UnknownState(t *testing.T) {
	s := NewStateStore()
	_, ok := s.Validate("non-existent-state")
	assert.False(t, ok)
}

// ── LoginHandler test ─────────────────────────────────────────────────────────

func TestLoginHandler_Redirects(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &OAuthConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		AuthURL:      "https://auth.example.com",
	}
	sessions := NewSessionStore("sid", 1*time.Hour)
	logger := zap.NewNop().Sugar()
	handler := NewOAuthHandler(cfg, sessions, logger)

	router := gin.New()
	router.GET("/auth/login", handler.LoginHandler)

	req := httptest.NewRequest(http.MethodGet, "/auth/login?redirect=/reports", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)

	location := w.Header().Get("Location")
	require.NotEmpty(t, location)

	parsed, err := url.Parse(location)
	require.NoError(t, err)

	// Must redirect to the XSUAA authorize endpoint.
	assert.Equal(t, "https://auth.example.com/oauth/authorize", parsed.Scheme+"://"+parsed.Host+parsed.Path)

	q := parsed.Query()
	assert.Equal(t, "test-client", q.Get("client_id"))
	assert.Equal(t, "code", q.Get("response_type"))
	assert.NotEmpty(t, q.Get("state"), "state param must be present for CSRF protection")
	// redirect_uri should be dynamically derived from the request host
	assert.Contains(t, q.Get("redirect_uri"), "/login/callback")
}

func TestLoginHandler_DefaultRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &OAuthConfig{
		ClientID:    "test-client",
		ClientSecret: "test-secret",
		AuthURL:     "https://auth.example.com",
	}
	sessions := NewSessionStore("sid", 1*time.Hour)
	logger := zap.NewNop().Sugar()
	handler := NewOAuthHandler(cfg, sessions, logger)

	router := gin.New()
	router.GET("/auth/login", handler.LoginHandler)

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)

	// The generated state should store "/" as the redirect.
	location := w.Header().Get("Location")
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	state := parsed.Query().Get("state")
	require.NotEmpty(t, state)

	redirectURI, ok := handler.states.Validate(state)
	require.True(t, ok)
	assert.Equal(t, "/", redirectURI)
}

// ── CallbackHandler test ──────────────────────────────────────────────────────

func TestCallbackHandler_ExchangesCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Spin up a mock XSUAA token endpoint.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"token_type":    "bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	// Point AuthURL at the mock server so the token exchange hits it.
	cfg := &OAuthConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		AuthURL:      tokenServer.URL,
	}
	sessions := NewSessionStore("sid", 1*time.Hour)
	logger := zap.NewNop().Sugar()
	handler := NewOAuthHandler(cfg, sessions, logger)

	// Pre-generate a valid state so CallbackHandler can validate it.
	state := handler.states.Generate("/")

	router := gin.New()
	router.GET("/auth/callback", handler.CallbackHandler)

	callbackURL := "/auth/callback?code=auth-code-xyz&state=" + url.QueryEscape(state)
	req := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Must redirect to the original URI.
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))

	// A session cookie must be set.
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessions.CookieName() {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "session cookie must be set after successful callback")

	// Retrieve the session and verify tokens.
	data, ok := sessions.Get(sessionCookie.Value)
	require.True(t, ok)
	assert.Equal(t, "mock-access-token", data.AccessToken)
	assert.Equal(t, "mock-refresh-token", data.RefreshToken)
}

func TestCallbackHandler_InvalidState(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &OAuthConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		AuthURL:      "https://auth.example.com",
	}
	sessions := NewSessionStore("sid", 1*time.Hour)
	logger := zap.NewNop().Sugar()
	handler := NewOAuthHandler(cfg, sessions, logger)

	router := gin.New()
	router.GET("/auth/callback", handler.CallbackHandler)

	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=some-code&state=invalid-state", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
