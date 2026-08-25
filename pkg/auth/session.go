package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// SessionData holds OAuth token state and cached user info for a session.
type SessionData struct {
	AccessToken  string
	RefreshToken string
	TokenExpiry  time.Time
	UserInfo     *UserInfoResponse // cached /userinfo result, nil initially
	CreatedAt    time.Time
}

// UserInfoResponse represents the payload returned by the OAuth /userinfo endpoint.
type UserInfoResponse struct {
	FirstName string      `json:"firstname"`
	LastName  string      `json:"lastname"`
	Email     string      `json:"email"`
	Name      string      `json:"name"`
	Groups    []UserGroup `json:"groups"`
}

// UserGroup is a single group entry inside UserInfoResponse.
type UserGroup struct {
	Value   string `json:"value"`
	Display string `json:"display"`
	Type    string `json:"type"`
}

// SessionStore is an in-memory session store protected by an HMAC-signed cookie.
// The cookie value is "<sessionID>.<HMAC-SHA256-hex>" so that the session ID
// cannot be forged without knowledge of the server-side HMAC key.
type SessionStore struct {
	mu         sync.RWMutex
	sessions   map[string]*SessionData
	hmacKey    []byte
	cookieName string
	maxAge     time.Duration
}

// NewSessionStore creates a SessionStore with a freshly generated HMAC key and
// starts a background cleanup goroutine that removes expired sessions every 5 minutes.
func NewSessionStore(cookieName string, maxAge time.Duration) *SessionStore {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// crypto/rand failure is unrecoverable at startup.
		panic("auth: failed to generate HMAC key: " + err.Error())
	}

	s := &SessionStore{
		sessions:   make(map[string]*SessionData),
		hmacKey:    key,
		cookieName: cookieName,
		maxAge:     maxAge,
	}
	go s.cleanupLoop()
	return s
}

// Create stores data under a new random session ID and returns the signed cookie value.
func (s *SessionStore) Create(data *SessionData) string {
	if data.CreatedAt.IsZero() {
		data.CreatedAt = time.Now()
	}

	id := generateID()

	s.mu.Lock()
	s.sessions[id] = data
	s.mu.Unlock()

	return id + "." + s.sign(id)
}

// Get verifies the cookie signature, checks whether the session exists and has not
// expired, and returns the session data if all checks pass.
func (s *SessionStore) Get(cookieValue string) (*SessionData, bool) {
	id, ok := s.verify(cookieValue)
	if !ok {
		return nil, false
	}

	s.mu.RLock()
	data, exists := s.sessions[id]
	s.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if s.maxAge > 0 && time.Since(data.CreatedAt) > s.maxAge {
		// Lazily remove the expired entry.
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		return nil, false
	}

	return data, true
}

// Delete verifies the cookie signature and removes the session from the store.
func (s *SessionStore) Delete(cookieValue string) {
	id, ok := s.verify(cookieValue)
	if !ok {
		return
	}

	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// SetCookie writes the session cookie to the Gin response with Secure and HttpOnly flags.
func (s *SessionStore) SetCookie(c *gin.Context, value string) {
	maxAgeSec := int(s.maxAge.Seconds())
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie expires the session cookie immediately (MaxAge = -1).
func (s *SessionStore) ClearCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     s.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// CookieName returns the name used for the session cookie.
func (s *SessionStore) CookieName() string {
	return s.cookieName
}

// sign returns the HMAC-SHA256 hex digest of id using the store's key.
func (s *SessionStore) sign(id string) string {
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

// verify splits cookieValue on "." and checks the HMAC.
// Returns the session ID and true if the signature is valid.
func (s *SessionStore) verify(cookieValue string) (string, bool) {
	idx := strings.LastIndex(cookieValue, ".")
	if idx < 0 {
		return "", false
	}
	id := cookieValue[:idx]
	sig := cookieValue[idx+1:]

	expected := s.sign(id)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", false
	}
	return id, true
}

// cleanupLoop runs in a goroutine and deletes expired sessions every 5 minutes.
func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for id, data := range s.sessions {
			if s.maxAge > 0 && time.Since(data.CreatedAt) > s.maxAge {
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
	}
}

// generateID returns a cryptographically random 32-character hex string (16 bytes).
func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("auth: failed to generate session ID: " + err.Error())
	}
	return hex.EncodeToString(b)
}
