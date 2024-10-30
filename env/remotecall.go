package env

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type HttpClient struct {
	Context      context.Context
	HttpClient   *http.Client
	AccessToken  string
	ApiURL       string
	ClientId     string
	ClientSecret string
	AuthUrl      string
}
type OauthResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Jti         string `json:"jti"`
}

type HttpRequest struct {
	Ctx         context.Context
	ApiURL      string
	Method      string
	RequestBody *bytes.Buffer
}

var cacheClient map[string]*HttpClient

// TODO: cache client, refresh token
func NewClient(ctx context.Context, clientID string, clientSecret string, authUrl string, apiUrl string) (*HttpClient, error) {
	if cacheClient == nil {
		cacheClient = make(map[string]*HttpClient)
	}
	if cacheClient[clientID] != nil {
		return cacheClient[clientID], nil
	}
	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", clientID, clientSecret))
	if !strings.HasSuffix(authUrl, "/oauth/token") {
		authUrl = fmt.Sprintf("%s/oauth/token", authUrl)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, authUrl, payload)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	httpClient := http.DefaultClient

	res, errReq := httpClient.Do(req)
	if errReq != nil {
		logger.Errorf("Error when get the response, %s", errReq)
		return nil, errReq
	}
	defer res.Body.Close()
	body, errIOReader := io.ReadAll(res.Body)
	if errIOReader != nil {
		logger.Errorf("Error when reading body from response, %s", errIOReader)
		return nil, errIOReader
	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		logger.Errorf("Error when extract json data from response, %s", jsonUnmarshalErr)
		return nil, jsonUnmarshalErr
	}
	client := &HttpClient{
		Context:      ctx,
		HttpClient:   httpClient,
		AccessToken:  oauthResp.AccessToken,
		ApiURL:       apiUrl,
		ClientId:     clientID,
		ClientSecret: clientSecret,
		AuthUrl:      authUrl,
	}
	cacheClient[clientID] = client
	return client, nil
}

func (c *HttpClient) Do(request *HttpRequest) (*[]byte, error) {
	childCtx, cancel := context.WithCancel(request.Ctx)
	defer cancel()
	var req *http.Request
	if request.RequestBody.String() == "<nil>" {
		req, _ = http.NewRequestWithContext(childCtx, request.Method, request.ApiURL, nil)
	} else {
		req, _ = http.NewRequestWithContext(childCtx, request.Method, request.ApiURL, request.RequestBody)
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}

	token := fmt.Sprintf("Bearer %s", c.AccessToken)
	req.Header.Add("Authorization", token)
	req.Header.Add("Accept", "application/json")
	resp, errReq := c.HttpClient.Do(req)

	if errReq != nil {
		logger.Errorf("Error when getting response from api, the error message is %s", errReq)
		return nil, errReq
	}
	defer resp.Body.Close()
	// refresh token
	if resp.StatusCode == 401 {
		logger.Error("Unauthorized. refresh token")
		delete(cacheClient, c.ClientId) // remove cache
		newClient, err := NewClient(c.Context, c.ClientId, c.ClientSecret, c.AuthUrl, c.ApiURL)
		if err != nil {
			logger.Errorf("Error when creating new client to refresh token: %s", err)
			return nil, err
		}
		return newClient.Do(request)
	}

	respBody, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		logger.Errorf("Error when getting  content from response, the error message is %s", errReq)
		return nil, errIOreader
	}
	return &respBody, nil
}
