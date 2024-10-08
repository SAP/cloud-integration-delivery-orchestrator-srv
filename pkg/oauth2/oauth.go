package oauth2

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var client_id = "74653741-4458-4cc6-902a-4681533d1509"
var client_secret = "REDACTED"

// https://accounts.sap.com/oauth2/authorize?
// client_id=9ba20f68-c7db-4901-b498-72b05a3851af&
// response_type=code&
// redirect_uri=https%3A%2F%2Fstage-devops.authentication.sap.hana.ondemand.com%2Flogin%2Fcallback%2Fldap&
// state=mHMOvXgzx9
// &scope=email+openid+profile
// &nonce=GJ0MxoCjdD6z

func Login(ctx *gin.Context) {
	response_type := "code"
	scope := "email+openid+profile"
	state := "state-maco-deploy"
	redirect_uri := "http://localhost:9000/auth"
	domain := "https://maco.accounts400.ondemand.com/oauth2/authorize"

	param := fmt.Sprintf("response_type=%s&scope=%s&client_id=%s&state=%s&redirect_uri=%s", response_type, scope, client_id, state, redirect_uri)

	auth_uri := fmt.Sprintf("%s?%s", domain, param)

	req, err := http.NewRequestWithContext(ctx, "GET", auth_uri, nil)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Authorization", fmt.Sprintf("Basic %s:%s", client_id, client_secret))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return
	}

	ctx.Data(http.StatusOK, "text/html; charset=utf-8", body)

}

func OauthCallback(ctx *gin.Context) {
	code := ctx.Query("code")
	state := ctx.Query("state")
	fmt.Println("state:", state)

	token_url := "https://maco.accounts400.ondemand.com/oauth2/token"
	payload := fmt.Sprintf("grant_type=authorization_code&code=%s&redirect_uri=%s&client_id=%s", code, "http://localhost:9000/auth", client_id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, token_url, strings.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Authorization", fmt.Sprintf("Basic %s:%s", client_id, client_secret))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return
	}
	fmt.Println(string(body))

}
