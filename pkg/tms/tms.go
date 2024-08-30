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

	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
)

var logger = log.NewLogger().Sugar()

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
	logger.Infof("Getting access token from %s\n", tmsAuthURL)
	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", clientID, clientSecret))

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tmsAuthURL, payload)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	httpClient := http.DefaultClient

	res, errReq := httpClient.Do(req)
	if errReq != nil {
		logger.Errorf("Error when get the response, %s", errReq)
		return &TMSClient{}, errReq
	}
	defer res.Body.Close()
	body, errIOReader := io.ReadAll(res.Body)
	if errIOReader != nil {
		logger.Errorf("Error when reading body from response, %s", errIOReader)
		return &TMSClient{}, errIOReader
	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		logger.Errorf("Error when extract json data from response, %s", jsonUnmarshalErr)
		return &TMSClient{}, jsonUnmarshalErr
	}
	return &TMSClient{
		context:     ctx,
		HTTPClient:  httpClient,
		AccessToken: oauthResp.AccessToken,
		TmsApiURL:   TmsApiURL,
	}, nil
}

type clientRequest struct {
	ctx         context.Context
	apiURL      string
	method      string
	requestBody *bytes.Buffer
}

func (t *TMSClient) Do(request clientRequest) ([]byte, error) {
	childCtx, cancel := context.WithCancel(request.ctx)
	defer cancel()

	var req *http.Request
	if request.requestBody.String() == "<nil>" {
		req, _ = http.NewRequestWithContext(childCtx, request.method, request.apiURL, nil)
	} else {
		req, _ = http.NewRequestWithContext(childCtx, request.method, request.apiURL, request.requestBody)
	}
	tokenHeaderVal := fmt.Sprintf("Bearer %s", t.AccessToken)
	req.Header.Add("Authorization", tokenHeaderVal)
	req.Header.Add("Accept", "application/json")
	resp, errReq := t.HTTPClient.Do(req)

	if errReq != nil {
		logger.Errorf("Error when getting response from api, the error message is %s", errReq)
		return []byte{}, errReq
	}
	defer resp.Body.Close()

	respBodyContent, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		logger.Errorf("Error when getting  content from response, the error message is %s", errReq)
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
	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/nodes", t.TmsApiURL)
	logger.Infof("Starting to get all tms nodes from %s\n", fullURL)
	request := clientRequest{
		ctx:    childCtx,
		apiURL: fullURL,
		method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []TMSNode{}, errReq
	}

	var tmsNodesResp TMSNodesResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &tmsNodesResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []TMSNode{}, jsonUnmarshalError
	}

	return tmsNodesResp.Nodes, nil

}

func (t *TMSClient) GetNodeID(nodeName string) int {
	var nodeID int
	nodes, err := t.GetNodes()
	if err != nil {
		logger.Errorf("Error when getting nodes, the error message is %s", err)
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
	logger.Infof("Starting to get tms node from  %s\n", fullURL)
	request := clientRequest{
		ctx:    childCtx,
		apiURL: fullURL,
		method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return TMSNode{}, errReq
	}

	var tmsNodeResp TMSNode
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &tmsNodeResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return TMSNode{}, jsonUnmarshalError
	}

	return tmsNodeResp, nil

}
func (t *TMSClient) GetNodeName(nodeID int) string {
	var nodeName string
	node, err := t.GetNode(nodeID)
	if err != nil {
		logger.Errorf("Error when getting node by id, the error message is %s", err)
		return nodeName
	}
	nodeName = node.Name

	return nodeName
}

type NodeTransportRequest struct {
	ID                 int       `json:"id"`
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

func (t *TMSClient) GetNodeTransportRequests(nodeID int) ([]NodeTransportRequest, error) {
	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%d/transportRequests?status=in,re,er,fa", t.TmsApiURL, nodeID)
	logger.Infof("Starting to get tranport requests for node %s from  %s\n", nodeID, fullURL)

	request := clientRequest{
		ctx:    childCtx,
		apiURL: fullURL,
		method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []NodeTransportRequest{}, errReq
	}

	var nodeTransportRequestsResp NodeTransportRequestsResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &nodeTransportRequestsResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []NodeTransportRequest{}, jsonUnmarshalError
	}

	return nodeTransportRequestsResp.TransportRequests, nil
}

type ReqImportTransportRequests struct {
	TransportRequests []int `json:"transportRequests"`
}

type ReqImportTransportResp struct {
	ActionID      int    `json:"actionId"`
	MonitoringURL string `json:"monitoringURL"`
}

func (t *TMSClient) ImportTransportRequest(nodeID int, transportRequestIDs []int) (int, error) {
	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%d/transportRequests/import", t.TmsApiURL, nodeID)
	var actionID int
	requestBodyContent := ReqImportTransportRequests{
		TransportRequests: transportRequestIDs,
	}

	requestBodyJson, _ := json.Marshal(requestBodyContent)
	logger.Infof("Starting to get all packages from cpi tenant %s\n", fullURL)

	request := clientRequest{
		ctx:         childCtx,
		apiURL:      fullURL,
		method:      http.MethodPost,
		requestBody: bytes.NewBuffer(requestBodyJson),
	}
	respBodyContent, errReq := t.Do(request)

	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return actionID, errReq
	}

	var reqImportTransportResp ReqImportTransportResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &reqImportTransportResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return actionID, jsonUnmarshalError
	}
	actionID = reqImportTransportResp.ActionID
	return actionID, nil
}

type ActionResultResp struct {
	ID                   int    `json:"id"`
	Type                 string `json:"type"`
	Status               string `json:"status"`
	StartedAt            string `json:"startedAt"`
	EndedAt              string `json:"endedAt"`
	TriggeredBy          string `json:"triggeredBy"`
	TriggeredByNamedUser string `json:"triggeredByNamedUser"`
	NodeName             string `json:"nodeName"`
	TransportRequests    []struct {
		ID       int    `json:"id"`
		Status   string `json:"status"`
		Entities []struct {
			ID       int    `json:"id"`
			FileName string `json:"fileName"`
			URI      string `json:"uri"`
			Status   string `json:"status"`
		} `json:"entities"`
	} `json:"transportRequests"`
}

func (t *TMSClient) GetActionResult(actionID int) (string, error) {

	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/actions/%d", t.TmsApiURL, actionID)
	request := clientRequest{
		ctx:    childCtx,
		apiURL: fullURL,
		method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return "", errReq

	}
	var actionResultResp ActionResultResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &actionResultResp)
	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return "", jsonUnmarshalError
	}
	return actionResultResp.Status, nil
}

type ActionLogResp struct {
	Logs []struct {
		TransportRequestID int    `json:"transportRequestId"`
		Status             string `json:"status"`
		Messages           []struct {
			ID        int    `json:"id"`
			MessageID string `json:"messageId"`
			Severity  string `json:"severity"`
			Message   string `json:"message"`
			CreatedAt string `json:"createdAt"`
		} `json:"messages"`
		Entities []struct {
			ID       int    `json:"id"`
			URI      string `json:"uri"`
			FileName string `json:"fileName"`
			Status   string `json:"status"`
			Messages []struct {
				ID        int    `json:"id"`
				MessageID string `json:"messageId"`
				Severity  string `json:"severity"`
				Message   string `json:"message"`
				CreatedAt string `json:"createdAt"`
			} `json:"messages"`
		} `json:"entities"`
	} `json:"logs"`
}

func (t *TMSClient) GetActionResultLog(actionID int) (ActionLogResp, error) {

	childCtx, cancel := context.WithCancel(t.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/actions/%d/logs", t.TmsApiURL, actionID)
	request := clientRequest{
		ctx:    childCtx,
		apiURL: fullURL,
		method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return ActionLogResp{}, errReq

	}
	var actionLogResp ActionLogResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &actionLogResp)
	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return ActionLogResp{}, jsonUnmarshalError
	}
	return actionLogResp, nil
}
