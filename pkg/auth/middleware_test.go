package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// fakeJWT builds a syntactically valid JWT (header.payload.fakesig) without
// a real cryptographic signature.  It is only suitable for tests that exercise
// the session-cookie or no-auth paths, where signature verification is skipped.
func fakeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + payloadB64 + ".fakesig"
}

// newTestStore returns a SessionStore with a short max-age suitable for tests.
func newTestStore() *SessionStore {
	return NewSessionStore("sid", 1*time.Hour)
}

// noopLogger returns a no-op zap.SugaredLogger for test use.
func noopLogger() *zap.SugaredLogger {
	return zap.NewNop().Sugar()
}

// ---------------------------------------------------------------------------
// Middleware tests
// ---------------------------------------------------------------------------

func TestMiddleware_SessionCookie(t *testing.T) {
	store := newTestStore()
	logger := noopLogger()

	// Create a fake access token with known claims.
	token := fakeJWT(map[string]any{
		"user_name": "alice",
		"scope":     []string{"uaa.read", "cpi.admin"},
		"origin":    "ldap",
		"user_id":   "u-001",
		"zid":       "zone-x",
	})

	cookie := store.Create(&SessionData{
		AccessToken:  token,
		RefreshToken: "rt",
		TokenExpiry:  time.Now().Add(1 * time.Hour),
	})

	// Build a test request with the session cookie.
	w := httptest.NewRecorder()
	c, router := gin.CreateTestContext(w)
	_ = router

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.AddCookie(&http.Cookie{Name: store.CookieName(), Value: cookie})
	c.Request = req

	called := false
	mw := Middleware(store, logger)
	mw(c)

	// Middleware should not abort.
	assert.False(t, c.IsAborted(), "middleware should not abort for valid session cookie")

	// Verify context keys.
	userName, exists := c.Get("user_name")
	require.True(t, exists, "user_name should be set")
	assert.Equal(t, "alice", userName)

	scope, exists := c.Get("scope")
	require.True(t, exists, "scope should be set")
	assert.Equal(t, []string{"uaa.read", "cpi.admin"}, scope)

	origin, exists := c.Get("origin")
	require.True(t, exists, "origin should be set")
	assert.Equal(t, "ldap", origin)

	at, exists := c.Get("access_token")
	require.True(t, exists, "access_token should be set")
	assert.Equal(t, token, at)

	_, exists = c.Get("uaa_claim")
	require.True(t, exists, "uaa_claim should be set")

	_ = called
}

func TestMiddleware_NoAuth(t *testing.T) {
	store := newTestStore()
	logger := noopLogger()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)

	mw := Middleware(store, logger)
	mw(c)

	assert.True(t, c.IsAborted(), "middleware should abort when no auth provided")
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "authentication required", body["message"])
}

func TestMiddleware_InvalidSessionCookie(t *testing.T) {
	store := newTestStore()
	logger := noopLogger()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	// Use a tampered / unknown cookie value.
	req.AddCookie(&http.Cookie{Name: store.CookieName(), Value: "invalid.cookie.value"})
	c.Request = req

	mw := Middleware(store, logger)
	mw(c)

	assert.True(t, c.IsAborted())
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------------------------------------------------------------------------
// SessionMiddleware tests
// ---------------------------------------------------------------------------

func TestSessionMiddleware_ValidSession(t *testing.T) {
	store := newTestStore()
	logger := noopLogger()

	token := fakeJWT(map[string]any{
		"user_name": "bob",
		"scope":     []string{"uaa.read"},
		"origin":    "sap.default",
	})
	cookie := store.Create(&SessionData{
		AccessToken: token,
		TokenExpiry: time.Now().Add(1 * time.Hour),
	})

	// Use a real router so that gin manages the handler chain and c.Next() works.
	nextCalled := false
	var capturedCtx *gin.Context

	router := gin.New()
	router.GET("/dashboard", SessionMiddleware(store, "/auth/login", logger), func(c *gin.Context) {
		nextCalled = true
		capturedCtx = c
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: store.CookieName(), Value: cookie})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "middleware should not abort for valid session")
	assert.True(t, nextCalled, "next handler should be called")

	require.NotNil(t, capturedCtx)
	userName, _ := capturedCtx.Get("user_name")
	assert.Equal(t, "bob", userName)

	at, _ := capturedCtx.Get("access_token")
	assert.Equal(t, token, at)
}

func TestSessionMiddleware_NoSession(t *testing.T) {
	store := newTestStore()
	logger := noopLogger()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/dashboard", nil)

	mw := SessionMiddleware(store, "/auth/login", logger)
	mw(c)

	assert.True(t, c.IsAborted(), "middleware should abort when no session")
	assert.Equal(t, http.StatusFound, w.Code)

	location := w.Header().Get("Location")
	assert.Contains(t, location, "/auth/login", "should redirect to login path")
	assert.Contains(t, location, "redirect=", "should include redirect param")
	assert.Contains(t, location, "%2Fdashboard", "redirect param should encode the original path")
}

func TestSessionMiddleware_InvalidCookie(t *testing.T) {
	store := newTestStore()
	logger := noopLogger()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: store.CookieName(), Value: "tampered.value"})
	c.Request = req

	mw := SessionMiddleware(store, "/auth/login", logger)
	mw(c)

	assert.True(t, c.IsAborted())
	assert.Equal(t, http.StatusFound, w.Code)
}

// ---------------------------------------------------------------------------
// parseClaimsFromToken unit tests
// ---------------------------------------------------------------------------

func TestParseClaimsFromToken_Valid(t *testing.T) {
	token := fakeJWT(map[string]any{
		"user_name": "charlie",
		"scope":     []string{"cpi.deploy"},
		"origin":    "ldap",
		"user_id":   "u-999",
		"zid":       "zone-y",
	})

	claims, err := parseClaimsFromToken(token)
	require.NoError(t, err)
	assert.Equal(t, "charlie", claims.UserName)
	assert.Equal(t, []string{"cpi.deploy"}, claims.Scope)
	assert.Equal(t, "ldap", claims.Origin)
	assert.Equal(t, "u-999", claims.UserID)
	assert.Equal(t, "zone-y", claims.ZoneID)
}

func TestParseClaimsFromToken_InvalidFormat(t *testing.T) {
	_, err := parseClaimsFromToken("not.a.valid.jwt.parts")
	assert.Error(t, err)

	_, err = parseClaimsFromToken("onlytwoparts")
	assert.Error(t, err)
}

func TestParseClaimsFromToken_BadBase64(t *testing.T) {
	_, err := parseClaimsFromToken("header.!!!invaldb64!!!.sig")
	assert.Error(t, err)
}
