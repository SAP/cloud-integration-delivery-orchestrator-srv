package oauth2

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/env"
)

var client_id = "e413f654a5f193da8bed"
var client_secret = "REDACTED"

func UserInfo(ctx *gin.Context) {
	code := ctx.Query("code")
	state := ctx.Query("state")
	redirect_uri := ctx.Query("callbackUrl")
	fmt.Println("state:", state)

	token_url := "https://github.wdf.sap.corp/login/oauth/access_token"
	payload := fmt.Sprintf("code=%s&redirect_uri=%s&client_id=%s&client_secret=%s", code, redirect_uri, client_id, client_secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, token_url, strings.NewReader(payload))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create request of access token: " + err.Error()})
		return
	}
	req.Header.Add("Accept", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to get access token: " + err.Error()})
		return
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to read response body: " + err.Error()})
		return
	}
	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to unmarshal token: " + err.Error()})
		return
	}

	// fetch user info
	user_url := "https://github.wdf.sap.corp/api/v3/user"
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, user_url, nil)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create request of user info: " + err.Error()})
		return
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		env.Logger().Error("Failed to get user info:", err)
		return
	}
	body, err = io.ReadAll(response.Body)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to read response body of user info: " + err.Error()})
		return
	}
	var userInfo User
	if err := json.Unmarshal(body, &userInfo); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to unmarshal user info: " + err.Error()})
		return
	}
	session := sessions.Default(ctx)
	// update session
	session.Set("User", userInfo)
	defer session.Save()
	ctx.JSON(http.StatusOK, gin.H{"result": userInfo})
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
}

type User struct {
	AvatarURL         string      `json:"avatar_url"`
	Bio               interface{} `json:"bio"`
	Blog              string      `json:"blog"`
	Company           interface{} `json:"company"`
	CreatedAt         time.Time   `json:"created_at"`
	Email             string      `json:"email"`
	EventsURL         string      `json:"events_url"`
	Followers         int         `json:"followers"`
	FollowersURL      string      `json:"followers_url"`
	Following         int         `json:"following"`
	FollowingURL      string      `json:"following_url"`
	GistsURL          string      `json:"gists_url"`
	GravatarID        string      `json:"gravatar_id"`
	Hireable          interface{} `json:"hireable"`
	HTMLURL           string      `json:"html_url"`
	ID                int         `json:"id"`
	Location          interface{} `json:"location"`
	Login             string      `json:"login"`
	Name              string      `json:"name"`
	NodeID            string      `json:"node_id"`
	OrganizationsURL  string      `json:"organizations_url"`
	PublicGists       int         `json:"public_gists"`
	PublicRepos       int         `json:"public_repos"`
	ReceivedEventsURL string      `json:"received_events_url"`
	ReposURL          string      `json:"repos_url"`
	SiteAdmin         bool        `json:"site_admin"`
	StarredURL        string      `json:"starred_url"`
	SubscriptionsURL  string      `json:"subscriptions_url"`
	SuspendedAt       interface{} `json:"suspended_at"`
	TwitterUsername   interface{} `json:"twitter_username"`
	Type              string      `json:"type"`
	UpdatedAt         time.Time   `json:"updated_at"`
	URL               string      `json:"url"`
}
