package remotecall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
)

var destinationMap map[string]Destination
var env *Env

func init() {
	env, _ = ReadEnv()
	err := destinations(env)
	if err != nil {
		return
	}

}

type Credentials struct {
	Tenantmode         string `json:"tenantmode"`
	Clientid           string `json:"clientid"`
	TokenServiceDomain string `json:"token_service_domain"`
	TokenServiceUrl    string `json:"token_service_url"`
	Clientsecret       string `json:"clientsecret"`
	Url                string `json:"url"`
	Uri                string `json:"uri"`
}

type Credentials_Tms struct {
	Uri string      `json:"uri"`
	Uaa Credentials `json:"uaa"`
}
type Env struct {
	VapServices map[string][]struct {
		Label        string                 `json:"label"`
		Plan         string                 `json:"plan"`
		Name         string                 `json:"name"`
		Tags         []string               `json:"tags"`
		InstanceGuid string                 `json:"instance_guid"`
		InstanceName string                 `json:"instance_name"`
		Credentials  map[string]interface{} `json:"credentials"`
	} `json:"VCAP_SERVICES"`
}

type HttpClient struct {
	Context     context.Context
	HttpClient  *http.Client
	AccessToken string
	ApiURL      string
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

type Destination struct {
	Name                string `json:"Name"`
	Type                string `json:"Type"`
	URL                 string `json:"URL"`
	Authentication      string `json:"Authentication"`
	ProxyType           string `json:"ProxyType"`
	TokenServiceURLType string `json:"tokenServiceURLType"`
	TrustAll            string `json:"TrustAll"`
	ClientId            string `json:"clientId"`
	ClientSecret        string `json:"clientSecret"`
	TokenServiceURL     string `json:"tokenServiceURL"`
}

type TMSEndpoint struct {
}

var logger = log.NewLogger().Sugar()

func NewClient(ctx context.Context, clientID string, clientSecret string, authUrl string, apiUrl string) (*HttpClient, error) {
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
	return &HttpClient{
		Context:     ctx,
		HttpClient:  httpClient,
		AccessToken: oauthResp.AccessToken,
		ApiURL:      apiUrl,
	}, nil
}

func (c *HttpClient) Do(request *HttpRequest) (*[]byte, error) {
	childCtx, cancel := context.WithCancel(request.Ctx)
	defer cancel()
	var req *http.Request
	if request.RequestBody.String() == "<nil>" {
		req, _ = http.NewRequestWithContext(childCtx, request.Method, request.ApiURL, nil)
	} else {
		req, _ = http.NewRequestWithContext(childCtx, request.Method, request.ApiURL, request.RequestBody)
	}

	token := fmt.Sprintf("Bearer %s", c.AccessToken)
	req.Header.Add("Authorization", token)
	req.Header.Add("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	resp, errReq := c.HttpClient.Do(req)

	if errReq != nil {
		logger.Errorf("Error when getting response from api, the error message is %s", errReq)
		return nil, errReq
	}
	defer resp.Body.Close()

	respBody, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		logger.Errorf("Error when getting  content from response, the error message is %s", errReq)
		return nil, errIOreader
	}
	return &respBody, nil
}

func ReadEnv() (*Env, error) {
	file, err := os.Open(".vscode/env.json")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var env Env
	if err := json.Unmarshal(content, &env); err != nil {
		return nil, err
	}
	return &env, err
}

// Get Destinations(including credentials)
func destinations(env *Env) error {
	ctx := context.Background()
	var credential Credentials
	mapstructure.Decode(env.VapServices["destination"][0].Credentials, &credential)
	apiUrl := credential.Uri
	authUrl := fmt.Sprintf("%s/oauth/token", credential.Url)
	client, err := NewClient(ctx,
		credential.Clientid,
		credential.Clientsecret,
		authUrl,
		apiUrl,
	)
	if err != nil {
		return err
	}
	apiUrl = fmt.Sprintf("%s/destination-configuration/v1/subaccountDestinations", apiUrl)
	req := &HttpRequest{
		Ctx:    ctx,
		ApiURL: apiUrl,
		Method: http.MethodGet,
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	var destinations []Destination
	if err := json.Unmarshal(*resp, &destinations); err != nil {
		return err
	}

	fmt.Print(destinations)
	m := make(map[string]Destination)
	for _, v := range destinations {
		m[v.Name] = v
	}
	destinationMap = m
	return nil
}

func DestEnv() map[string]Destination {
	return destinationMap
}

// tms environment
func TmsEnv() Credentials_Tms {
	var credential Credentials_Tms
	mapstructure.Decode(env.VapServices["transport"][0].Credentials, &credential)
	return credential
}
