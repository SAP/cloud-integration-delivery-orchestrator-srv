package env

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type HttpClient struct {
	HttpClient   *http.Client
	AccessToken  string
	ApiURL       string
	ClientId     string
	ClientSecret string
	AuthUrl      string
	mu           sync.Mutex // protects AccessToken
}
type OauthResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Jti         string `json:"jti"`
}

type HttpRequest struct {
	ApiURL      string
	Method      string
	RequestBody *bytes.Buffer
}

func NewClient(ctx context.Context, clientID string, clientSecret string, authUrl string, apiUrl string) (*HttpClient, error) {
	if !strings.HasSuffix(authUrl, "/oauth/token") {
		authUrl = fmt.Sprintf("%s/oauth/token", authUrl)
	}
	client := &HttpClient{
		HttpClient:   http.DefaultClient,
		ApiURL:       apiUrl,
		ClientId:     clientID,
		ClientSecret: clientSecret,
		AuthUrl:      authUrl,
	}
	if err := client.fetchToken(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// fetchToken fetches a new OAuth token and stores it on the client (mutex-protected).
func (c *HttpClient) fetchToken(ctx context.Context) error {
	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", c.ClientId, c.ClientSecret))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.AuthUrl, payload)
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	res, errReq := c.HttpClient.Do(req)
	if errReq != nil {
		logger.Errorf("Error when get the response, %s", errReq)
		return errReq
	}
	defer res.Body.Close()
	body, errIOReader := io.ReadAll(res.Body)
	if errIOReader != nil {
		logger.Errorf("Error when reading body from response, %s", errIOReader)
		return errIOReader
	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		logger.Errorf("Error when extract json data from response, %s", jsonUnmarshalErr)
		return jsonUnmarshalErr
	}

	c.mu.Lock()
	c.AccessToken = oauthResp.AccessToken
	c.mu.Unlock()
	return nil
}

// Do executes an HTTP request with the given context and returns the response body, status code, and error.
// On 401, it refreshes the token once and retries.
func (c *HttpClient) Do(ctx context.Context, request *HttpRequest) (*[]byte, int, error) {
	respBody, statusCode, err := c.doRequest(ctx, request)
	if err != nil {
		return nil, 0, err
	}

	// refresh token on 401 (retry once, no infinite recursion)
	if statusCode == 401 {
		logger.Error("Unauthorized. refreshing token")
		if err := c.fetchToken(ctx); err != nil {
			logger.Errorf("Error when refreshing token: %s", err)
			return nil, 0, err
		}
		return c.doRequest(ctx, request)
	}

	return respBody, statusCode, nil
}

func (c *HttpClient) doRequest(ctx context.Context, request *HttpRequest) (*[]byte, int, error) {
	var req *http.Request
	if request.RequestBody == nil || request.RequestBody.String() == "<nil>" {
		req, _ = http.NewRequestWithContext(ctx, request.Method, request.ApiURL, nil)
	} else {
		req, _ = http.NewRequestWithContext(ctx, request.Method, request.ApiURL, request.RequestBody)
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}

	c.mu.Lock()
	token := fmt.Sprintf("Bearer %s", c.AccessToken)
	c.mu.Unlock()

	req.Header.Add("Authorization", token)
	req.Header.Add("Accept", "application/json")
	resp, errReq := c.HttpClient.Do(req)

	if errReq != nil {
		logger.Errorf("Error when getting response from api, the error message is %s", errReq)
		return nil, 0, errReq
	}
	defer resp.Body.Close()

	respBody, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		logger.Errorf("Error when getting content from response, the error message is %s", errIOreader)
		return nil, 0, errIOreader
	}
	return &respBody, resp.StatusCode, nil
}
