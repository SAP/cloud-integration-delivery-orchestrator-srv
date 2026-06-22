package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionStore_CreateAndGet(t *testing.T) {
	store := NewSessionStore("test_sid", 1*time.Hour)
	data := &SessionData{AccessToken: "at-123", RefreshToken: "rt-456", TokenExpiry: time.Now().Add(1 * time.Hour)}

	cookie := store.Create(data)
	assert.NotEmpty(t, cookie)

	got, ok := store.Get(cookie)
	require.True(t, ok)
	assert.Equal(t, "at-123", got.AccessToken)
	assert.Equal(t, "rt-456", got.RefreshToken)
}

func TestSessionStore_TamperedCookie(t *testing.T) {
	store := NewSessionStore("test_sid", 1*time.Hour)
	cookie := store.Create(&SessionData{AccessToken: "at"})

	_, ok := store.Get(cookie + "x")
	assert.False(t, ok)

	_, ok = store.Get("fake.signature")
	assert.False(t, ok)
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewSessionStore("test_sid", 1*time.Hour)
	cookie := store.Create(&SessionData{AccessToken: "x"})

	store.Delete(cookie)
	_, ok := store.Get(cookie)
	assert.False(t, ok)
}

func TestSessionStore_Expired(t *testing.T) {
	store := NewSessionStore("test_sid", 1*time.Millisecond)
	cookie := store.Create(&SessionData{AccessToken: "x"})

	time.Sleep(5 * time.Millisecond)
	_, ok := store.Get(cookie)
	assert.False(t, ok)
}
