package tms

import (
	"context"
	"encoding/json"
	"fmt"
	"mmt-delivery/db"
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
	Type       string        `json:"type"`
	Name       *string       `json:"name"`
	Version    *string       `json:"version"`
	ParentID   *int64        `json:"parentId"`
	Attributes []interface{} `json:"attributes"`
}

type File struct {
	FileID   string `json:"fileId"`
	FileName string `json:"fileName"`
	FileSize int64  `json:"fileSize"`
	MD5      string `json:"md5"`
}

// /v1/transportRequests/{TrNumber}
// NOTE: this api is not from api hub
func (t *TmsClient) GetTransportRequest(TrNumber string) (*TransportRequestV1, error) {
	childCtx, cancel := context.WithCancel(t.Context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/transportRequests/%s?expand=logs,landscape", t.ApiURL, TrNumber)
	logger.Infof("Starting to get tr info: %s\n", fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullURL,
		Method: http.MethodGet,
	}
	body, err := t.Do(&request)
	if err != nil {
		return nil, fmt.Errorf("error when getting transport request %s, error message %s", TrNumber, err)
	}
	var tr TransportRequestV1
	if err := json.Unmarshal(*body, &tr); err != nil {
		return nil, fmt.Errorf("error when unmarshalling transport request %s, error message %s", TrNumber, err)
	}
	return &tr, nil

}

// update Import status of an artifact in each transport node.
// status can be(from TMS): SUCCEEDED, INITIAL, FATAL, etc...
func (t *TmsClient) UpdateArtifactStatus(artifact *db.Artifact) error {
	tr, err := t.GetTransportRequest(artifact.TransportRequestNumber)
	if err != nil {
		return fmt.Errorf("failed to get transport request %s: %s", artifact.TransportRequestNumber, err)
	}
	status := "UNKNOWN"
	for _, node := range tr.Landscape.Nodes {
		if node.State == nil {
			continue
		}
		status = node.State.Status
		transportNodeId, transportNodeName := node.ID, node.Name
		artifact.NodeStatus[fmt.Sprint(transportNodeId)] = db.TransportNodeStatus{
			TransportNodeName: transportNodeName,
			Status:            status,
			UpdatedAt:         node.State.Time,
		}
	}

	// TODO: update overall status based on individual node status

	return nil
}
