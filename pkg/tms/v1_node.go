package tms

// v1_node.go — TMS v1 node management API types and TmsClient methods.
//
// The TMS node management endpoints (GET /v1/nodes, GET /v1/routes) are not
// published on the SAP API hub.  They were discovered via browser network
// inspection and are used here under the team's accepted risk decision
// (RFC 013 OP-01).  All calls are centralised in this file so any future API
// change can be addressed in a single place.
//
// V1NodeClient has been retired (I5): GetNodeByName and ListRoutesBySourceNode
// are now methods on *TmsClient, constructed via NewTmsClient with credentials
// resolved at runtime from the provider Destination Service.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"mmt-delivery/pkg/env"
)

// ── Data types ────────────────────────────────────────────────────────────────

// V1TransportNode represents a TMS source node returned by the v1 node API.
type V1TransportNode struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Targets     []NodeTarget `json:"targets"`
}

// NodeTarget is a single deployment target within a TMS node.
type NodeTarget struct {
	ID              int64         `json:"id,omitempty"`
	ContentType     string        `json:"contentType"`
	DestinationName string        `json:"destinationName"`
	NodeID          int64         `json:"nodeId,omitempty"`
	ImportOptions   ImportOptions `json:"importOptions"`
}

// ImportOptions holds the import strategy for a NodeTarget.
type ImportOptions struct {
	Strategy string `json:"strategy"` // "default"
}

// V1TransportRoute is a route between two nodes in the TMS landscape.
type V1TransportRoute struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	SourceNode RouteNodeRef `json:"sourceNode"`
	TargetNode RouteNodeRef `json:"targetNode"`
}

// RouteNodeRef is the partial node reference embedded in a route.
type RouteNodeRef struct {
	ID int64 `json:"id"`
}

// ── TmsClient v1 node methods ─────────────────────────────────────────────────

// GetNodeByName returns the TMS v1 node with the given name, or nil if no
// node with that name exists.
//
// API: GET /v1/nodes?name={name}
func (t *TmsClient) GetNodeByName(ctx context.Context, name string) (*V1TransportNode, error) {
	fullURL := fmt.Sprintf("%s/v1/nodes?name=%s", t.ApiURL, url.QueryEscape(name))
	request := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}
	data, status, err := t.Do(ctx, request)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("tms: GetNodeByName %q returned HTTP %d: %s", name, status, string(*data))
	}

	// The TMS v1 GET /v1/nodes?name= endpoint can return either a single node
	// object or a JSON array depending on whether query parameters are supported.
	// Try single-object first, then array fallback.
	var single V1TransportNode
	if err := json.Unmarshal(*data, &single); err == nil && single.ID != 0 {
		return &single, nil
	}

	var list []V1TransportNode
	if err := json.Unmarshal(*data, &list); err != nil {
		return nil, fmt.Errorf("tms: GetNodeByName: decode node list: %w", err)
	}
	for _, n := range list {
		if n.Name == name {
			return &n, nil
		}
	}
	return nil, nil
}

// ListRoutesBySourceNode returns all routes that have the given node ID as
// their source.
//
// API: GET /v1/routes (filtered client-side by sourceNode.id)
func (t *TmsClient) ListRoutesBySourceNode(ctx context.Context, sourceNodeID int64) ([]V1TransportRoute, error) {
	fullURL := fmt.Sprintf("%s/v1/routes", t.ApiURL)
	request := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}
	data, status, err := t.Do(ctx, request)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("tms: ListRoutesBySourceNode returned HTTP %d: %s", status, string(*data))
	}

	var all []V1TransportRoute
	if err := json.Unmarshal(*data, &all); err != nil {
		// Some TMS versions wrap the response.
		var wrapper struct {
			Routes []V1TransportRoute `json:"routes"`
		}
		if err2 := json.Unmarshal(*data, &wrapper); err2 != nil {
			return nil, fmt.Errorf("tms: ListRoutesBySourceNode: decode routes: %w", err)
		}
		all = wrapper.Routes
	}

	var result []V1TransportRoute
	for _, r := range all {
		if r.SourceNode.ID == sourceNodeID {
			result = append(result, r)
		}
	}
	return result, nil
}
