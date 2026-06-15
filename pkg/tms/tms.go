package tms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/env"

	"go.uber.org/zap"
)

// logger returns the package logger, resolved lazily via env.Logger().
// Safe to call before env.Init() — returns a fallback logger.
func logger() *zap.SugaredLogger { return env.Logger() }

type TMSNodesResp struct {
	Nodes []db.TransportNode `json:"nodes"`
}
type TmsClient struct {
	*env.HttpClient
}

// NewTmsClient constructs a TmsClient from explicit OAuth credentials resolved
// at runtime from the provider Destination Service (CentralTmsContext.TmsApiDestinationName).
// This is the preferred constructor; use it for all new call sites.
func NewTmsClient(ctx context.Context, apiEndpoint, tokenURL, clientID, clientSecret string) (*TmsClient, error) {
	client, err := env.NewClient(ctx, clientID, clientSecret, tokenURL, apiEndpoint)
	if err != nil {
		return nil, err
	}
	return &TmsClient{client}, nil
}

func (t *TmsClient) GetNodes(ctx context.Context) ([]db.TransportNode, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/v2/nodes", t.ApiURL)
	logger().Infof("Starting to get all tms nodes from %s\n", fullURL)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetNodes request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return []db.TransportNode{}, errReq
	}

	var tmsNodesResp TMSNodesResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &tmsNodesResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []db.TransportNode{}, jsonUnmarshalError
	}

	return tmsNodesResp.Nodes, nil

}

func (t *TmsClient) GetNodeID(ctx context.Context, nodeName string) uint {
	var nodeID uint
	nodes, err := t.GetNodes(ctx)
	if err != nil {
		logger().Errorf("Error when getting nodes, the error message is %s", err)
		return nodeID
	}

	for _, node := range nodes {
		if node.Name == nodeName {
			nodeID = node.ID
		}
	}
	return nodeID
}

func (t *TmsClient) GetNode(ctx context.Context, nodeID uint) (db.TransportNode, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v2/nodes/%d", t.ApiURL, nodeID)
	logger().Infof("Starting to get tms node from %s\n", fullURL)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetNode request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return db.TransportNode{}, errReq
	}

	var tmsNodeResp db.TransportNode
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &tmsNodeResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return db.TransportNode{}, jsonUnmarshalError
	}

	return tmsNodeResp, nil

}
func (t *TmsClient) GetNodeName(ctx context.Context, nodeID uint) string {
	var nodeName string
	node, err := t.GetNode(ctx, nodeID)
	if err != nil {
		logger().Errorf("Error when getting node by id, the error message is %s", err)
		return nodeName
	}
	nodeName = node.Name

	return nodeName
}

type TMSRoutesResp struct {
	Routes []db.TransportRoute `json:"routes"`
}

// TODO: this is not a official API from TMS api hub.
func (t *TmsClient) GetRoutes(ctx context.Context) ([]db.TransportRoute, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/v2/routes", t.ApiURL)
	logger().Infof("Starting to get all tms routes from %s\n", fullURL)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetRoutes request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content of tms routes, the error message is %s", errReq)
		return []db.TransportRoute{}, errReq
	}

	var tmsRoutesResp TMSRoutesResp
	if err := json.Unmarshal(*respBodyContent, &tmsRoutesResp); err != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", err)
		return []db.TransportRoute{}, err
	}
	return tmsRoutesResp.Routes, nil
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

func (t *TmsClient) GetNodeTransportRequests(ctx context.Context, nodeID uint) ([]NodeTransportRequest, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v2/nodes/%d/transportRequests?status=in,re,er,fa", t.ApiURL, nodeID)
	logger().Infof("Starting to get transport requests for node %d from %s\n", nodeID, fullURL)

	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetNodeTransportRequests request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return []NodeTransportRequest{}, errReq
	}

	var nodeTransportRequestsResp NodeTransportRequestsResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &nodeTransportRequestsResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []NodeTransportRequest{}, jsonUnmarshalError
	}

	return nodeTransportRequestsResp.TransportRequests, nil
}

type ReqImportTransportRequests struct {
	TransportRequests []uint `json:"transportRequests"`
}

type ReqImportTransportResp struct {
	ActionID      int    `json:"actionId"`
	MonitoringURL string `json:"monitoringURL"`
}

func (t *TmsClient) ImportTransportRequest(ctx context.Context, nodeID uint, transportRequestIDs []uint) (uint, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v2/nodes/%d/transportRequests/import", t.ApiURL, nodeID)
	var actionID uint
	requestBodyContent := ReqImportTransportRequests{
		TransportRequests: transportRequestIDs,
	}

	requestBodyJson, _ := json.Marshal(requestBodyContent)
	logger().Infof("Starting to import transport requests to node %d: %s\n", nodeID, fullURL)

	request := env.HttpRequest{
		ApiURL:      fullURL,
		Method:      http.MethodPost,
		RequestBody: bytes.NewBuffer(requestBodyJson),
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)

	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("Import request timeout after %v: %s", consts.ImportTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return actionID, errReq
	}

	var reqImportTransportResp ReqImportTransportResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &reqImportTransportResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return actionID, jsonUnmarshalError
	}
	if reqImportTransportResp.ActionID == 0 {
		logger().Errorf("Error when getting action id, the response is %s", reqImportTransportResp)
		return actionID, fmt.Errorf("failed to trigger import: %s", string(*respBodyContent))
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
// also return endedAt, if status is not running
func (t *TmsClient) GetActionResult(ctx context.Context, actionID uint) (string, string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/v2/actions/%d", t.ApiURL, actionID)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetActionResult request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return "", "", errReq
	}
	var actionResultResp ActionResultResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &actionResultResp)
	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return "", "", jsonUnmarshalError
	}
	return actionResultResp.Status, actionResultResp.EndedAt, nil
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

func (t *TmsClient) GetActionResultLog(ctx context.Context, actionID uint) (ActionLogResp, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.LongRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/v2/actions/%d/logs", t.ApiURL, actionID)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetActionResultLog request timeout after %v: %s", consts.LongRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return ActionLogResp{}, errReq
	}
	var actionLogResp ActionLogResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &actionLogResp)
	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return ActionLogResp{}, jsonUnmarshalError
	}
	return actionLogResp, nil
}

type TransportLog struct {
	Logs []struct {
		ActionID          int       `json:"actionId"`
		ActionType        string    `json:"actionType"`
		Status            string    `json:"status"`
		ActionStartedAt   time.Time `json:"actionStartedAt"`
		ActionTriggeredBy string    `json:"actionTriggeredBy"`
		Messages          []struct {
			ID        int       `json:"id"`
			MessageID string    `json:"messageId"`
			Severity  string    `json:"severity"`
			Message   string    `json:"message"`
			CreatedAt time.Time `json:"createdAt"`
		} `json:"messages"`
		Entities []struct {
			ID       int    `json:"id"`
			URI      string `json:"uri"`
			Status   string `json:"status"`
			FileName string `json:"fileName"`
			Messages []struct {
				ID        int       `json:"id"`
				MessageID string    `json:"messageId"`
				Severity  string    `json:"severity"`
				Message   string    `json:"message"`
				CreatedAt time.Time `json:"createdAt"`
			} `json:"messages"`
		} `json:"entities"`
	} `json:"logs"`
}

func (t *TmsClient) getTransportLogs(ctx context.Context, trNumber string, nodeID uint) (TransportLog, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.LongRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/v2/nodes/%d/transportRequests/%s/logs", t.ApiURL, nodeID, trNumber)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, _, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("getTransportLogs request timeout after %v: %s", consts.LongRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return TransportLog{}, errReq
	}
	var transportLogResp TransportLog
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &transportLogResp)
	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return TransportLog{}, jsonUnmarshalError
	}
	return transportLogResp, nil
}

func (t *TmsClient) ErrLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) (errLogs []string, err error) {
	errLogs = make([]string, 0)
	transportLogResp, err := t.getTransportLogs(ctx, trNumber, nodeID)
	if err != nil {
		return
	}
	for _, trLog := range transportLogResp.Logs {
		for _, entity := range trLog.Entities {
			for _, msg := range entity.Messages {
				if msg.Severity == "F" || msg.Severity == "E" {
					errLogs = append(errLogs, fmt.Sprintf("Transport Request %s failed in Node %d: %s", trNumber, nodeID, msg.Message))
				}
			}
		}
	}
	return
}

// WarnLogsInTransportLog returns messages with severity "W" from the transport request log.
func (t *TmsClient) WarnLogsInTransportLog(ctx context.Context, trNumber string, nodeID uint) (warnLogs []string, err error) {
	warnLogs = make([]string, 0)
	transportLogResp, err := t.getTransportLogs(ctx, trNumber, nodeID)
	if err != nil {
		return
	}
	for _, trLog := range transportLogResp.Logs {
		for _, entity := range trLog.Entities {
			for _, msg := range entity.Messages {
				if msg.Severity == "W" {
					warnLogs = append(warnLogs, fmt.Sprintf("Transport Request %s in Node %d (warning): %s", trNumber, nodeID, msg.Message))
				}
			}
		}
	}
	return
}
