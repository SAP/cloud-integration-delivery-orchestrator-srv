package tms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type TMSClient struct {
	context     context.Context
	HTTPClient  *http.Client
	AccessToken string
	TmsApiURL   string
}
type OauthResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Jti         string `json:"jti"`
}

func NewTMSClient(ctx context.Context, clientID string, clientSecret string, tmsAuthURL string, TmsApiURL string) (*TMSClient, error) {
	log.Printf("Getting access token from %s\n", tmsAuthURL)
	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", clientID, clientSecret))

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tmsAuthURL, payload)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	httpClient := http.DefaultClient

	res, errReq := httpClient.Do(req)
	if errReq != nil {
		log.Printf("Error when get the response, %s", errReq)
		return &TMSClient{}, errReq
	}
	defer res.Body.Close()
	body, errIOReader := io.ReadAll(res.Body)
	if errIOReader != nil {
		log.Printf("Error when reading body from response, %s", errIOReader)
		return &TMSClient{}, errIOReader
	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		log.Printf("Error when extract json data from response, %s", jsonUnmarshalErr)
		return &TMSClient{}, jsonUnmarshalErr
	}
	return &TMSClient{
		context:     ctx,
		HTTPClient:  httpClient,
		AccessToken: oauthResp.AccessToken,
		TmsApiURL:   TmsApiURL,
	}, nil
}

func (t *TMSClient) Get(ctx context.Context, apiURL string) ([]byte, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(childCtx, http.MethodGet, apiURL, nil)
	tokenHeaderVal := fmt.Sprintf("Bearer %s", t.AccessToken)
	req.Header.Add("Authorization", tokenHeaderVal)
	req.Header.Add("Accept", "application/json")
	resp, errReq := t.HTTPClient.Do(req)

	if errReq != nil {
		log.Printf("Error when getting response from api, the error message is %s", errReq)
		return []byte{}, errReq
	}
	defer resp.Body.Close()

	respBodyContent, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		log.Printf("Error when getting  content from response, the error message is %s", errReq)
		return []byte{}, errIOreader
	}
	return respBodyContent, nil
}

func (t *TMSClient) Post(ctx context.Context, apiURL string, requestBody *bytes.Buffer) ([]byte, error) {
	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()
	req, _ := http.NewRequestWithContext(childCtx, http.MethodPost, apiURL, requestBody)
	tokenHeaderVal := fmt.Sprintf("Bearer %s", t.AccessToken)
	req.Header.Add("Authorization", tokenHeaderVal)
	req.Header.Add("Accept", "application/json")
	resp, errReq := t.HTTPClient.Do(req)

	if errReq != nil {
		log.Printf("Error when getting response from api, the error message is %s", errReq)
		return []byte{}, errReq
	}
	defer resp.Body.Close()

	respBodyContent, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		log.Printf("Error when getting  content from response, the error message is %s", errReq)
		return []byte{}, errIOreader
	}
	return respBodyContent, nil
}

type TMSNode struct {
	ID                   int    `json:"id"`
	Description          string `json:"description"`
	Name                 string `json:"name"`
	UploadAllowed        bool   `json:"uploadAllowed"`
	NotificationEnabled  bool   `json:"notificationEnabled"`
	ForwardMode          string `json:"forwardMode"`
	ImportDisabled       bool   `json:"importDisabled"`
	ImportDisabledReason string `json:"importDisabledReason"`
	Targets              []struct {
		ID              int    `json:"id"`
		ContentType     string `json:"contentType"`
		DestinationName string `json:"destinationName"`
		ImportOptions   struct {
			Strategy string `json:"strategy"`
		} `json:"importOptions"`
	} `json:"targets"`
	Virtual bool `json:"virtual"`
}

type TMSNodesResp struct {
	Nodes []TMSNode `json:"nodes"`
}

func (t *TMSClient) GetNodes() ([]TMSNode, error) {
	ctx, cancel := context.WithCancel(t.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/nodes", t.TmsApiURL)
	log.Printf("Starting to get all tms nodes from %s\n", fullURL)
	respBodyContent, errReq := t.Get(ctx, fullURL)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return []TMSNode{}, errReq
	}

	var tmsNodesResp TMSNodesResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &tmsNodesResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []TMSNode{}, jsonUnmarshalError
	}

	return tmsNodesResp.Nodes, nil

}

func (t *TMSClient) GetNodeID(nodeName string) int {
	var nodeID int
	nodes, err := t.GetNodes()
	if err != nil {
		log.Printf("Error when getting nodes, the error message is %s", err)
		return nodeID
	}

	for _, node := range nodes {
		if node.Name == nodeName {
			nodeID = node.ID
		}
	}
	return nodeID
}

func (t *TMSClient) GetNode(nodeID int) (TMSNode, error) {
	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%d", t.TmsApiURL, nodeID)
	log.Printf("Starting to get tms node from  %s\n", fullURL)
	respBodyContent, errReq := t.Get(childCtx, fullURL)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return TMSNode{}, errReq
	}

	var tmsNodeResp TMSNode
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &tmsNodeResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return TMSNode{}, jsonUnmarshalError
	}

	return tmsNodeResp, nil

}
func (t *TMSClient) GetNodeName(nodeID int) string {
	var nodeName string
	node, err := t.GetNode(nodeID)
	if err != nil {
		log.Printf("Error when getting node by id, the error message is %s", err)
		return nodeName
	}
	nodeName = node.Name

	return nodeName
}

type NodeTransportRequest struct {
	ID                 string    `json:"id"`
	Status             string    `json:"status"`
	Archived           bool      `json:"archived"`
	Position           int       `json:"position"`
	CreatedBy          string    `json:"createdBy"`
	CreatedByNamedUser string    `json:"createdByNamedUser"`
	CreatedAt          time.Time `json:"createdAt"`
	QueuedAt           time.Time `json:"queuedAt"`
	Description        string    `json:"description"`
	Origin             string    `json:"origin"`
	Entries            []struct {
		ID          int    `json:"id"`
		StorageType string `json:"storageType"`
		ContentType string `json:"contentType"`
		URI         string `json:"uri"`
	} `json:"entries"`
}

type NodeTransportRequestsResp struct {
	TransportRequests []NodeTransportRequest `json:"transportRequests"`
}

func (t *TMSClient) GetNodeTransportRequests(nodeID string) ([]NodeTransportRequest, error) {
	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%s/transportRequests?status=in,re,er,fa", t.TmsApiURL, nodeID)
	log.Printf("Starting to get tranport requests for node %s from  %s\n", nodeID, fullURL)
	respBodyContent, errReq := t.Get(childCtx, fullURL)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return []NodeTransportRequest{}, errReq
	}

	var nodeTransportRequestsResp NodeTransportRequestsResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &nodeTransportRequestsResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []NodeTransportRequest{}, jsonUnmarshalError
	}

	return nodeTransportRequestsResp.TransportRequests, nil
}

type ReqImportTransportRequests struct {
	TransportRequests []int `json:"transportRequests"`
}

type ReqImportTransportResp struct {
	ActionID      string `json:"actionId"`
	MonitoringURL string `json:"monitoringURL"`
}

func (t *TMSClient) ImportTransportRequest(nodeID string, transportRequestIDs []int) (string, error) {
	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%s/transportRequests/import", t.TmsApiURL, nodeID)
	var actionID string
	requestBodyContent := ReqImportTransportRequests{
		TransportRequests: transportRequestIDs,
	}

	requestBodyJson, _ := json.Marshal(requestBodyContent)

	log.Printf("Starting to get all packages from cpi tenant %s\n", fullURL)
	respBodyContent, errReq := t.Post(childCtx, fullURL, bytes.NewBuffer(requestBodyJson))
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return actionID, errReq
	}

	var reqImportTransportResp ReqImportTransportResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &reqImportTransportResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return actionID, jsonUnmarshalError
	}
	actionID = reqImportTransportResp.ActionID
	return actionID, nil
}
