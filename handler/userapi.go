package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"mmt-delivery/pkg/auth"
)

// HandleCurrentUser implements GET /user-api/currentUser.
// It calls XSUAA /userinfo endpoint with the user's access token, caches the result
// in the session, and returns flat JSON (NO {data:...} wrapper) matching SAP Approuter contract.
func HandleCurrentUser(sessions *auth.SessionStore, userInfoURL string) gin.HandlerFunc {
	httpClient := &http.Client{Timeout: 10 * time.Second}

	return func(c *gin.Context) {
		// Get access token from context (set by SessionMiddleware)
		accessToken, exists := c.Get("access_token")
		if !exists {
			c.JSON(401, gin.H{"message": "authentication required"})
			return
		}
		token := accessToken.(string)

		// Check session cache
		cookieValue, _ := c.Cookie(sessions.CookieName())
		if session, ok := sessions.Get(cookieValue); ok && session.UserInfo != nil {
			// Return cached userinfo — flat JSON, no wrapper
			c.JSON(200, session.UserInfo)
			return
		}

		// Call XSUAA /userinfo endpoint
		req, err := http.NewRequestWithContext(c.Request.Context(), "GET", userInfoURL, nil)
		if err != nil {
			c.JSON(500, gin.H{"message": "failed to build userinfo request"})
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := httpClient.Do(req)
		if err != nil {
			c.JSON(502, gin.H{"message": fmt.Sprintf("failed to call userinfo endpoint: %s", err)})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			c.JSON(502, gin.H{"message": fmt.Sprintf("userinfo returned %d: %s", resp.StatusCode, string(body))})
			return
		}

		var raw map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			c.JSON(500, gin.H{"message": "failed to decode userinfo response"})
			return
		}

		// Map XSUAA /userinfo response to our format
		userInfo := &auth.UserInfoResponse{
			FirstName: getString(raw, "given_name"),
			LastName:  getString(raw, "family_name"),
			Email:     getString(raw, "email"),
			Name:      getString(raw, "user_name"),
		}
		// Groups come from xs.system.attributes or external_groups
		if groups, ok := raw["xs.system.attributes"].(map[string]interface{}); ok {
			if roleCollections, ok := groups["xs.rolecollections"].([]interface{}); ok {
				for _, rc := range roleCollections {
					if name, ok := rc.(string); ok {
						userInfo.Groups = append(userInfo.Groups, auth.UserGroup{
							Value: name, Display: name, Type: "DIRECT",
						})
					}
				}
			}
		}

		// Cache in session
		if session, ok := sessions.Get(cookieValue); ok {
			session.UserInfo = userInfo
		}

		// Return flat JSON (matching SAP Approuter contract)
		c.JSON(200, userInfo)
	}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
