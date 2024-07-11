package cpi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type CPIClient struct {
	context context.Context
	AccessToken string
	CpiAPI      string
}

type OauthResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn     int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Jti         string `json:"jti"`
}

func NewCPIClient(ctx context.Context, clientID string, clientSecret string, cpiAuthURL string) *CPIClient {

	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", clientID, clientSecret))

	req, _ := http.NewRequest("POST", cpiAuthURL, payload)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Errorf("Error when get the response, %s", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Errorf("Error when reading body from response, %s", err)

	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		fmt.Errorf("Error when extract jsib data from response, %s", jsonUnmarshalErr)
	}

	return &CPIClient{
		context: ctx,
		AccessToken: oauthResp.AccessToken,
		CpiAPI:      cpiAuthURL,
	}
}

func ( c *CPIClient )  GetPackage() {


}