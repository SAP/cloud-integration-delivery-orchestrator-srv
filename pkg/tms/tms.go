package tms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.wdf.sap.corp/maco-mmt/maco-deploy/env"
)

var logger = env.Logger()

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
type TmsClient struct {
	env.HttpClient
}

func NewClient(ctx context.Context) (*TmsClient, error) {
	v := env.TmsCredential()
	apiUrl := fmt.Sprintf("%s/v2", v.ApiUrl)
	client, err := env.NewClient(ctx, v.Clientid, v.Clientsecret, v.AuthUrl, apiUrl)
	return &TmsClient{*client}, err
}

func (t *TmsClient) GetNodes() ([]TMSNode, error) {
	childCtx, cancel := context.WithCancel(t.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/nodes", t.ApiURL)
	logger.Infof("Starting to get all tms nodes from %s\n", fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []TMSNode{}, errReq
	}

	var tmsNodesResp TMSNodesResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &tmsNodesResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []TMSNode{}, jsonUnmarshalError
	}

	return tmsNodesResp.Nodes, nil

}

func (t *TmsClient) GetNodeID(nodeName string) int {
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

func (t *TmsClient) GetNode(nodeID int) (TMSNode, error) {
	childCtx, cancel := context.WithCancel(t.Context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%d", t.ApiURL, nodeID)
	logger.Infof("Starting to get tms node from  %s\n", fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return TMSNode{}, errReq
	}

	var tmsNodeResp TMSNode
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &tmsNodeResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return TMSNode{}, jsonUnmarshalError
	}

	return tmsNodeResp, nil

}
func (t *TmsClient) GetNodeName(nodeID int) string {
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

func (t *TmsClient) GetNodeTransportRequests(nodeID int) ([]NodeTransportRequest, error) {
	childCtx, cancel := context.WithCancel(t.Context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%d/transportRequests?status=in,re,er,fa", t.ApiURL, nodeID)
	logger.Infof("Starting to get tranport requests for node %s from  %s\n", nodeID, fullURL)

	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response content, the error message is %s", errReq)
		return []NodeTransportRequest{}, errReq
	}

	var nodeTransportRequestsResp NodeTransportRequestsResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &nodeTransportRequestsResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []NodeTransportRequest{}, jsonUnmarshalError
	}

	return nodeTransportRequestsResp.TransportRequests, nil
}

type ReqImportTransportRequests struct {
	TransportRequests []int32 `json:"transportRequests"`
}

type ReqImportTransportResp struct {
	ActionID      int    `json:"actionId"`
	MonitoringURL string `json:"monitoringURL"`
}

func (t *TmsClient) ImportTransportRequest(nodeID uint, transportRequestIDs []int32) (uint, error) {
	childCtx, cancel := context.WithCancel(t.Context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/nodes/%d/transportRequests/import", t.ApiURL, nodeID)
	var actionID uint
	requestBodyContent := ReqImportTransportRequests{
		TransportRequests: transportRequestIDs,
	}

	requestBodyJson, _ := json.Marshal(requestBodyContent)
	logger.Infof("Starting to get all packages from cpi tenant %s\n", fullURL)

	request := env.HttpRequest{
		Ctx:         childCtx,
		ApiURL:      fullURL,
		Method:      http.MethodPost,
		RequestBody: bytes.NewBuffer(requestBodyJson),
	}
	respBodyContent, errReq := t.Do(&request)

	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return actionID, errReq
	}

	var reqImportTransportResp ReqImportTransportResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &reqImportTransportResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return actionID, jsonUnmarshalError
	}
	actionID = uint(reqImportTransportResp.ActionID)
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

// succeeded, warning, error, fatal, running, initial, unknown
func (t *TmsClient) GetActionResult(actionID uint) (string, error) {
	childCtx, cancel := context.WithCancel(t.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/actions/%d", t.ApiURL, actionID)
	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return "", errReq

	}
	var actionResultResp ActionResultResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &actionResultResp)
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

func (t *TmsClient) GetActionResultLog(actionID uint) (ActionLogResp, error) {

	childCtx, cancel := context.WithCancel(t.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/actions/%d/logs", t.ApiURL, actionID)
	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return ActionLogResp{}, errReq

	}
	var actionLogResp ActionLogResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &actionLogResp)
	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return ActionLogResp{}, jsonUnmarshalError
	}
	return actionLogResp, nil
}
