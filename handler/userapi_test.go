package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/auth"
)

func TestHandleCurrentUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Mock XSUAA /userinfo endpoint
	mockUserInfo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Bearer token is forwarded
		assert.Equal(t, "Bearer test-access-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"given_name":  "John",
			"family_name": "Doe",
			"email":       "john.doe@sap.com",
			"user_name":   "john.doe",
			"xs.system.attributes": map[string]interface{}{
				"xs.rolecollections": []interface{}{"CPI Delivery Admin", "CPI Delivery Operator"},
			},
		})
	}))
	defer mockUserInfo.Close()

	sessions := auth.NewSessionStore("test_sid", 1*time.Hour)
	cookie := sessions.Create(&auth.SessionData{AccessToken: "test-access-token"})

	handler := HandleCurrentUser(sessions, mockUserInfo.URL)

	// Build request with session cookie and access_token in context
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/user-api/currentUser", nil)
	c.Request.AddCookie(&http.Cookie{Name: "test_sid", Value: cookie})
	c.Set("access_token", "test-access-token")

	handler(c)

	assert.Equal(t, 200, w.Code)

	// Verify response is FLAT JSON (no {data:...} wrapper)
	var resp auth.UserInfoResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "John", resp.FirstName)
	assert.Equal(t, "Doe", resp.LastName)
	assert.Equal(t, "john.doe@sap.com", resp.Email)
	assert.Equal(t, "john.doe", resp.Name)
	assert.Len(t, resp.Groups, 2)
	assert.Equal(t, "CPI Delivery Admin", resp.Groups[0].Display)
}

func TestHandleCurrentUser_CachesResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	callCount := 0
	mockUserInfo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"given_name": "Jane", "family_name": "Smith", "email": "jane@sap.com", "user_name": "jane",
		})
	}))
	defer mockUserInfo.Close()

	sessions := auth.NewSessionStore("test_sid", 1*time.Hour)
	cookie := sessions.Create(&auth.SessionData{AccessToken: "tok"})
	handler := HandleCurrentUser(sessions, mockUserInfo.URL)

	// First call
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/user-api/currentUser", nil)
	c.Request.AddCookie(&http.Cookie{Name: "test_sid", Value: cookie})
	c.Set("access_token", "tok")
	handler(c)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, 1, callCount)

	// Second call — should use cache
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("GET", "/user-api/currentUser", nil)
	c2.Request.AddCookie(&http.Cookie{Name: "test_sid", Value: cookie})
	c2.Set("access_token", "tok")
	handler(c2)
	assert.Equal(t, 200, w2.Code)
	assert.Equal(t, 1, callCount) // No additional call to XSUAA
}

func TestHandleCurrentUser_NoAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := auth.NewSessionStore("test_sid", 1*time.Hour)
	handler := HandleCurrentUser(sessions, "http://unused")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/user-api/currentUser", nil)
	handler(c)

	assert.Equal(t, 401, w.Code)
}
