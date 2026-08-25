package tms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
)

type TMSNodesResp struct {
	Nodes []db.TransportNode `json:"nodes"`
}
type TmsClient struct {
	*env.HttpClient
	sem chan struct{} // global concurrency limit for TMS requests
}

// maxConcurrentTMSRequests limits parallel outgoing requests to the shared TMS endpoint.
// TMS is a central SAP service with strict rate limiting; keep this conservative.
const maxConcurrentTMSRequests = 3

// NewTmsClient constructs a TmsClient from explicit OAuth credentials resolved
// at runtime from the provider Destination Service (CentralTmsContext.TmsApiDestinationName).
// This is the preferred constructor; use it for all new call sites.
func NewTmsClient(ctx context.Context, apiEndpoint, tokenURL, clientID, clientSecret string) (*TmsClient, error) {
	client, err := env.NewClient(ctx, clientID, clientSecret, tokenURL, apiEndpoint)
	if err != nil {
		return nil, err
	}
	return &TmsClient{HttpClient: client, sem: make(chan struct{}, maxConcurrentTMSRequests)}, nil
}

// Do wraps HttpClient.Do with global concurrency limiting.
func (t *TmsClient) Do(ctx context.Context, request *env.HttpRequest) ([]byte, error) {
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return t.HttpClient.Do(ctx, request)
}

func (t *TmsClient) GetNodes(ctx context.Context) ([]db.TransportNode, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/v2/nodes", t.ApiURL)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		return []db.TransportNode{}, fmt.Errorf("GetNodes: %w", errReq)
	}

	var tmsNodesResp TMSNodesResp
	if err := json.Unmarshal(respBodyContent, &tmsNodesResp); err != nil {
		return []db.TransportNode{}, fmt.Errorf("GetNodes unmarshal: %w", err)
	}

	return tmsNodesResp.Nodes, nil

}

func (t *TmsClient) GetNodeID(ctx context.Context, nodeName string) uint {
	var nodeID uint
	nodes, err := t.GetNodes(ctx)
	if err != nil {
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
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		return db.TransportNode{}, fmt.Errorf("GetNode: %w", errReq)
	}

	var tmsNodeResp db.TransportNode
	if err := json.Unmarshal(respBodyContent, &tmsNodeResp); err != nil {
		return db.TransportNode{}, fmt.Errorf("GetNode unmarshal: %w", err)
	}

	return tmsNodeResp, nil

}
func (t *TmsClient) GetNodeName(ctx context.Context, nodeID uint) string {
	var nodeName string
	node, err := t.GetNode(ctx, nodeID)
	if err != nil {
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
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		return []db.TransportRoute{}, fmt.Errorf("GetRoutes: %w", errReq)
	}

	var tmsRoutesResp TMSRoutesResp
	if err := json.Unmarshal(respBodyContent, &tmsRoutesResp); err != nil {
		return []db.TransportRoute{}, fmt.Errorf("GetRoutes unmarshal: %w", err)
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

	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	respBodyContent, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		return []NodeTransportRequest{}, fmt.Errorf("GetNodeTransportRequests: %w", errReq)
	}

	var nodeTransportRequestsResp NodeTransportRequestsResp
	if err := json.Unmarshal(respBodyContent, &nodeTransportRequestsResp); err != nil {
		return []NodeTransportRequest{}, fmt.Errorf("GetNodeTransportRequests unmarshal: %w", err)
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

	request := env.HttpRequest{
		ApiURL:      fullURL,
		Method:      http.MethodPost,
		RequestBody: requestBodyJson,
	}
	respBodyContent, errReq := t.Do(childCtx, &request)

	if errReq != nil {
		return actionID, fmt.Errorf("ImportTransportRequest: %w", errReq)
	}

	var reqImportTransportResp ReqImportTransportResp
	if err := json.Unmarshal(respBodyContent, &reqImportTransportResp); err != nil {
		return actionID, fmt.Errorf("ImportTransportRequest unmarshal: %w", err)
	}
	if reqImportTransportResp.ActionID == 0 {
		return actionID, fmt.Errorf("ImportTransportRequest: failed to trigger import: %s", string(respBodyContent))
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
	respBodyContent, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		return "", "", fmt.Errorf("GetActionResult: %w", errReq)
	}
	var actionResultResp ActionResultResp
	if err := json.Unmarshal(respBodyContent, &actionResultResp); err != nil {
		return "", "", fmt.Errorf("GetActionResult unmarshal: %w", err)
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
	respBodyContent, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		return ActionLogResp{}, fmt.Errorf("GetActionResultLog: %w", errReq)
	}
	var actionLogResp ActionLogResp
	if err := json.Unmarshal(respBodyContent, &actionLogResp); err != nil {
		return ActionLogResp{}, fmt.Errorf("GetActionResultLog unmarshal: %w", err)
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
	respBodyContent, errReq := t.Do(childCtx, &request)
	if errReq != nil {
		return TransportLog{}, fmt.Errorf("getTransportLogs: %w", errReq)
	}
	var transportLogResp TransportLog
	if err := json.Unmarshal(respBodyContent, &transportLogResp); err != nil {
		return TransportLog{}, fmt.Errorf("getTransportLogs unmarshal: %w", err)
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
