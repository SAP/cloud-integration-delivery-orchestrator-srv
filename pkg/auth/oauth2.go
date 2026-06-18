package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

// stateTTL is how long a CSRF state value remains valid.
const stateTTL = 5 * time.Minute

// stateEntry holds the creation time and the original URL the user was trying to reach.
type stateEntry struct {
	createdAt   time.Time
	redirectURI string
}

// StateStore is an in-memory, short-lived store for OAuth2 CSRF state parameters.
// Each state is valid for stateTTL (5 minutes) and is deleted on first use.
type StateStore struct {
	mu     sync.Mutex
	states map[string]stateEntry
}

// NewStateStore creates a StateStore and starts a background cleanup goroutine.
func NewStateStore() *StateStore {
	s := &StateStore{
		states: make(map[string]stateEntry),
	}
	go s.cleanupLoop()
	return s
}

// Generate creates a random 32-character hex state, stores it with redirectURI,
// and returns the state string.
func (s *StateStore) Generate(redirectURI string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("auth: failed to generate state: " + err.Error())
	}
	state := hex.EncodeToString(b)

	s.mu.Lock()
	s.states[state] = stateEntry{createdAt: time.Now(), redirectURI: redirectURI}
	s.mu.Unlock()

	return state
}

// Validate checks that state exists and has not expired.
// On success it deletes the state (one-time use) and returns the original redirectURI.
func (s *StateStore) Validate(state string) (redirectURI string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.states[state]
	if !exists {
		return "", false
	}
	if time.Since(entry.createdAt) > stateTTL {
		delete(s.states, state)
		return "", false
	}
	delete(s.states, state)
	return entry.redirectURI, true
}

// cleanupLoop evicts entries older than stateTTL every 5 minutes.
func (s *StateStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for state, entry := range s.states {
			if time.Since(entry.createdAt) > stateTTL {
				delete(s.states, state)
			}
		}
		s.mu.Unlock()
	}
}

// OAuthHandler provides Gin handlers for the OAuth2 Authorization Code Flow.
type OAuthHandler struct {
	config    *OAuthConfig
	oauth2Cfg *oauth2.Config
	sessions  *SessionStore
	states    *StateStore
	logger    *zap.SugaredLogger
}

// NewOAuthHandler constructs an OAuthHandler wired to the given OAuthConfig and SessionStore.
func NewOAuthHandler(cfg *OAuthConfig, sessions *SessionStore, logger *zap.SugaredLogger) *OAuthHandler {
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  cfg.AuthorizeURL(),
			TokenURL: cfg.TokenURL(),
		},
		// RedirectURL intentionally NOT set here — derived dynamically per request
		// (same behavior as SAP Approuter)
		// Scopes intentionally empty — let XSUAA grant all user-entitled scopes
	}

	return &OAuthHandler{
		config:    cfg,
		oauth2Cfg: oauth2Cfg,
		sessions:  sessions,
		states:    NewStateStore(),
		logger:    logger,
	}
}

// buildCallbackURL derives the OAuth2 redirect_uri from the current request.
// Uses X-Forwarded-Proto (set by CF GoRouter) for scheme, falls back to "http" for local dev.
// This matches SAP Approuter behavior — no hardcoded host/URL needed.
func buildCallbackURL(c *gin.Context) string {
	scheme := c.GetHeader("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host + "/login/callback"
}

// LoginHandler handles GET /auth/login.
// It reads an optional `redirect` query param (default "/"), generates a CSRF state,
// and redirects the browser to the XSUAA authorization endpoint.
func (h *OAuthHandler) LoginHandler(c *gin.Context) {
	redirectURI := c.Query("redirect")
	if redirectURI == "" {
		redirectURI = "/"
	}

	state := h.states.Generate(redirectURI)
	callbackURL := buildCallbackURL(c)

	// Build authorize URL manually — XSUAA expects params without percent-encoding
	// (SAP quirk: rejects %21 for '!' in client_id, and encoded redirect_uri).
	// This matches SAP Approuter's behavior of sending raw values.
	// NOTE: no scope parameter — let XSUAA return all scopes the user is entitled to
	// (matching Approuter behavior; requesting only "openid" would strip app scopes).
	authURL := h.config.AuthorizeURL() +
		"?response_type=code" +
		"&client_id=" + h.config.ClientID +
		"&redirect_uri=" + callbackURL +
		"&state=" + state

	c.Redirect(302, authURL)
}

// CallbackHandler handles GET /auth/callback.
// It validates the CSRF state, exchanges the authorization code for tokens,
// stores a session, sets the session cookie, and redirects to the original URI.
func (h *OAuthHandler) CallbackHandler(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")

	originalURI, ok := h.states.Validate(state)
	if !ok {
		h.logger.Warnw("oauth2 callback: invalid or expired state", "state", state)
		c.JSON(400, gin.H{"error": "invalid_state", "message": "CSRF state mismatch or expired"})
		return
	}

	// Exchange code for tokens, passing the same redirect_uri used in the authorize request
	callbackURL := buildCallbackURL(c)
	token, err := h.oauth2Cfg.Exchange(context.Background(), code, oauth2.SetAuthURLParam("redirect_uri", callbackURL))
	if err != nil {
		h.logger.Errorw("oauth2 callback: token exchange failed", "error", err)
		c.JSON(502, gin.H{"error": "token_exchange_failed", "message": "failed to exchange authorization code"})
		return
	}

	data := &SessionData{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenExpiry:  token.Expiry,
		CreatedAt:    time.Now(),
	}

	cookieValue := h.sessions.Create(data)
	h.sessions.SetCookie(c, cookieValue)

	if originalURI == "" {
		originalURI = "/"
	}
	c.Redirect(302, originalURI)
}
