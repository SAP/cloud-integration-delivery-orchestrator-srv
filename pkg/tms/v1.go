package tms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	. "mmt-delivery/consts"
	"mmt-delivery/pkg/env"
	"net/http"
	"time"
)

type TransportRequestV1 struct {
	Description          string    `json:"description"`
	Origin               string    `json:"origin"`
	Owner                string    `json:"owner"`
	UploadToOrigin       bool      `json:"uploadToOrigin"`
	PreferredContentType string    `json:"preferredContentType"`
	Content              []Content `json:"content"`
	ID                   int64     `json:"id"`
	State                string    `json:"state"` // RELEASED, ARCHIVED
	Size                 int64     `json:"size"`
	CreatedAt            time.Time `json:"createdAt"`
	Landscape            struct {
		Nodes  []Node   `json:"nodes"` // there will be TR status in each node
		Routes []Routes `json:"routes"`
	}
}

type Node struct {
	ID          uint       `json:"id"`
	Name        string     `json:"name"`
	ForwardMode string     `json:"forwardMode"`
	Test        bool       `json:"test"`
	Production  bool       `json:"production"`
	Virtual     bool       `json:"virtual"`
	State       *NodeState `json:"state,omitempty"` // be be
}

type NodeState struct {
	ID       uint      `json:"id"`
	Position uint      `json:"position"`
	Time     time.Time `json:"time"`
	Status   string    `json:"status"`
	Archived bool      `json:"archived"`
	Test     bool      `json:"test"`
}

type Routes struct {
	ID           int64 `json:"id"`
	SourceNodeID int64 `json:"sourceNodeId"`
	TargetNodeID int64 `json:"targetNodeId"`
}

type Content struct {
	ContentType string     `json:"contentType"`
	StorageType string     `json:"storageType"`
	Metadata    []Metadata `json:"metadata"`
	File        *File      `json:"file"`
	ID          int64      `json:"id"`
	Deleted     bool       `json:"deleted"`
	Position    int        `json:"position"`
}

type Metadata struct {
	ID         int64         `json:"id"`
	EntityID   string        `json:"entityId"`
	Type       ArtifactType  `json:"type"`
	Name       string        `json:"name"`
	Version    string        `json:"version"`
	ParentID   int64         `json:"parentId"`
	Attributes []interface{} `json:"attributes"`
}

type File struct {
	FileID   string `json:"fileId"`
	FileName string `json:"fileName"`
	FileSize int64  `json:"fileSize"`
	MD5      string `json:"md5"`
}

// status of an artifact in each transport node, should check by tr number
type TrNodeStatus struct {
	TransportRequestNumber string    `json:"transportRequestNumber"`
	StateID                uint      `json:"id"` // state id
	TransportNodeID        uint      `json:"transportNodeId"`
	TransportNodeName      string    `json:"TransportNodeName"`
	Status                 string    `json:"status"` // SUCCEEDED, INITIAL, FATAL, RUNNING, WARNING, ERROR, etc...
	UpdatedAt              time.Time `json:"updatedAt"`
}

// check status of a single TR. /v1/transportRequests/{TrNumber}
// NOTE: this api is not from api hub. may not be stable
func (t *TmsClient) GetTransportRequest(ctx context.Context, TrNumber string) (*TransportRequestV1, error) {
	childCtx, cancel := context.WithTimeout(ctx, DefaultRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/transportRequests/%s?expand=logs,landscape", t.ApiURL, TrNumber)
	logger().Infof("Starting to get tr info: %s\n", fullURL)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	body, err := t.Do(childCtx, &request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger().Errorf("GetTransportRequest timeout after %v: %s", DefaultRequestTimeout, fullURL)
		}
		return nil, fmt.Errorf("error when getting transport request %s, error message %s", TrNumber, err)
	}
	var tr TransportRequestV1
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("error when unmarshalling transport request %s, error message %s", TrNumber, err)
	}
	return &tr, nil

}

// update Import status of an artifact in each transport node.
// status can be(from TMS): SUCCEEDED, INITIAL(when imported into next node. eg: dev -> ci, then state in ci should be inital),
// FATAL, RUNNING, etc...
func (t *TmsClient) TrNodeStatuses(ctx context.Context, trNumber string) (map[uint]TrNodeStatus, error) {
	tr, err := t.GetTransportRequest(ctx, trNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get transport request %s: %s", trNumber, err)
	}
	// if a transport request number exists, there should be at least one node, the next node will be INITIAL
	nodeStatus := make(map[uint]TrNodeStatus) // key: transportNodeId
	for _, node := range tr.Landscape.Nodes {
		if node.State == nil {
			continue
		}
		status, stateID := node.State.Status, node.State.ID
		transportNodeId, transportNodeName := node.ID, node.Name
		nodeStatus[transportNodeId] = TrNodeStatus{
			TransportRequestNumber: trNumber,
			StateID:                stateID,
			TransportNodeID:        transportNodeId,
			TransportNodeName:      transportNodeName,
			Status:                 status,
			UpdatedAt:              node.State.Time,
		}
	}
	if len(nodeStatus) == 0 {
		return nil, fmt.Errorf("no node status found for transport request %s", trNumber)
	}
	return nodeStatus, nil
}
