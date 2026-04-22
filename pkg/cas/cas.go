package cas

// cas.go — Content Agent Service (CAS) client (Phase 4).
//
// CasClient is per-tenant: each CpiTenant has its own CAS engine endpoint and
// OAuth credentials stored in CpiTenant.CasEngineDestinationName.  TrResolver
// builds a fresh CasClient per GenerateTransportRequest call — no caching.
// Contrast with TmsClient (central, one CentralTmsContext record per deployment).
//
// CasClient embeds *env.HttpClient to reuse lazy token fetch, mutex protection,
// and 401 auto-retry — no separate OAuth implementation here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"mmt-delivery/consts"
	"mmt-delivery/pkg/env"

	"go.uber.org/zap"
)

func logger() *zap.SugaredLogger { return env.Logger() }

// ── Client ────────────────────────────────────────────────────────────────────

type CasClient struct {
	*env.HttpClient
}

// NewCasClient constructs a CasClient from explicit OAuth credentials resolved
// at runtime from the provider Destination Service (CpiTenant.CasEngineDestinationName).
func NewCasClient(_ context.Context, apiEndpoint, tokenURL, clientID, clientSecret string) (*CasClient, error) {
	client, err := env.NewClient(context.Background(), clientID, clientSecret, tokenURL, apiEndpoint)
	if err != nil {
		return nil, err
	}
	return &CasClient{client}, nil
}

// ── Catalog types (GET /v1/contentResources response) ────────────────────────

// CatalogContentResource is one entry from GET /v1/contentResources.
// The service layer uses this to resolve artifact GUIDs and package metadata
// before building an ExportRequest.  Only "package" subType entries are relevant
// for TR generation; "Destination" entries are ignored.
type CatalogContentResource struct {
	ID           string             `json:"id"`          // package tech ID (e.g. "AlexTest")
	ResourceID   string             `json:"resourceID"`  // package GUID (e.g. "d8e90a3b...")
	Type         string             `json:"type"`        // "Cloud Integration"
	Name         string             `json:"name"`        // package display name
	SubType      string             `json:"subType"`     // "package" | "Destination"
	Version      string             `json:"version"`     // package version
	Components   []CatalogComponent `json:"components"`
	Dependencies []any              `json:"dependencies"`
}

// CatalogComponent is one artifact entry within a CatalogContentResource.
// component.name == Artifact.TechID is the matching key.
// component.id is the artifact GUID required by ExportRequest.
type CatalogComponent struct {
	ID         string `json:"id"`         // artifact GUID
	Name       string `json:"name"`       // artifact tech ID — matches Artifact.TechID in our DB
	Type       string `json:"type"`       // "IFlow" | "ScriptCollection" | "OData Service" | "IntegrationAdapter" | …
	Version    string `json:"version"`    // current version in CAS
	Exportable bool   `json:"exportable"`
}

// ── Export request/response types ─────────────────────────────────────────────

// ExportRequest is the body for POST /v1/contentResources/export.
// No top-level "id" field: that field is a CAS UI browser-session artifact
// (requestor=CASUI).  Server-to-server calls with requestor=CPIDelivery omit it.
type ExportRequest struct {
	Requestor            string            `json:"requestor"`            // fixed "CPIDelivery"
	Version              string            `json:"version"`              // fixed "1.0.0"
	ExportMode           string            `json:"exportMode"`           // fixed "TransportManagementService"
	ExportMediaType      string            `json:"exportMediaType"`      // fixed "MTAR"
	Description          string            `json:"description"`          // "Delivery Request #<ID> — <Name>"
	ContentResources     []ContentResource `json:"contentResources"`
	SourceNode           string            `json:"sourceNode"`           // CpiTenant.TmsSourceNodeName
	TransportDestination string            `json:"transportDestination"` // fixed "TransportManagementService" (v1)
	IsModifiable         bool              `json:"isModifiable"`         // fixed false
}

// ContentResource is one Cloud Integration package in an ExportRequest.
// Fields are populated from the CAS catalog (CatalogContentResource) to ensure
// correct GUIDs and package metadata.
type ContentResource struct {
	ID                   string                     `json:"id"`          // package tech ID
	ResourceID           string                     `json:"resourceID"`  // package GUID
	ContentType          string                     `json:"contentType"` // fixed "Cloud Integration"
	SubType              string                     `json:"subType"`     // fixed "package"
	Type                 string                     `json:"type"`        // fixed "Cloud Integration"
	Name                 string                     `json:"name"`
	Version              string                     `json:"version"`     // package version from catalog
	Components           []ContentResourceComponent `json:"components"`
	Dependencies         []any                      `json:"dependencies"`          // fixed []
	MtaDescriptorSpecific MtaDescriptorSpecific     `json:"mtaDescriptorSpecific"` // fixed {"deployed-after":[]}
}

// MtaDescriptorSpecific is the fixed MTA metadata appended to each ContentResource.
type MtaDescriptorSpecific struct {
	DeployedAfter []any `json:"deployed-after"` // fixed []
}

// ContentResourceComponent is one artifact entry within a ContentResource.
// id must be the artifact GUID from the CAS catalog (not the tech ID).
type ContentResourceComponent struct {
	ID                   string      `json:"id"`                   // artifact GUID from catalog
	Name                 string      `json:"name"`                 // artifact tech ID / display name
	Type                 string      `json:"type"`                 // "IFlow" | "ScriptCollection" | …
	Version              string      `json:"version"`              // version from ArtifactTenantOperation
	Selected             bool        `json:"selected"`             // fixed true
	Enabled              bool        `json:"enabled"`              // fixed true
	Mandatory            bool        `json:"mandatory"`            // fixed false
	DefaultSelect        bool        `json:"defaultSelect"`        // fixed false
	AdditionalProperties any        `json:"additionalProperties"` // fixed null
	Exportable           bool        `json:"exportable"`           // from catalog
}

// ExportResponse is returned by POST /v1/contentResources/export.
type ExportResponse struct {
	ActivityID  string `json:"activityId"`
	ProcessID   string `json:"processId"` // used for subsequent polling
	ProcessType string `json:"processType"`
	StartedAt   string `json:"startedAt"`
	State       string `json:"state"`    // INITIAL on creation
	Progress    int    `json:"progress"` // 0 on creation
}

// OperationStatus is returned by GET /v1/operations/{processId}?messages=true.
// Poll until State = "FINISHED"; abort immediately on State = "FAILED".
type OperationStatus struct {
	ActivityID  string             `json:"activityId"`
	ProcessID   string             `json:"processId"`
	ProcessType string             `json:"processType"`
	StartedAt   string             `json:"startedAt"`
	EndedAt     string             `json:"endedAt,omitempty"`
	State       string             `json:"state"`             // INITIAL / STARTED / RUNNING / FINISHED / FAILED
	Progress    int                `json:"progress"`          // 0–100
	Messages    []OperationMessage `json:"messages,omitempty"`
}

// OperationMessage is a single log entry within OperationStatus.Messages.
type OperationMessage struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Type      string `json:"type"`      // INFO / ERROR / WARNING
	Timestamp string `json:"timestamp"`
}

// OperationConfig is returned by GET /v1/operations/{processId}/config?logs=true.
// Call only after State = "FINISHED"; read TransportRequestID as the primary TR ID.
type OperationConfig struct {
	ActivityID          string `json:"activityID"`
	FileID              string `json:"fileID"`
	TransportRequestID  string `json:"transportRequestID"`  // primary TR ID source
	TransportRequestURL string `json:"transportRequestURL"` // audit / UI link
	TrStatus            struct {
		Nodes []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"nodes"`
	} `json:"trStatus"`
}

// Activity is one entry from GET /v1/activities.
// Auxiliary audit path; not the primary TR ID source.
type Activity struct {
	ActivityID    string `json:"activityId"`
	ProcessID     string `json:"processId"`
	Requestor     string `json:"requestor"`
	State         string `json:"state"`
	StartedAt     string `json:"startedAt"`
	TransportInfo struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"transportInfo"`
}

// ── API methods ───────────────────────────────────────────────────────────────

// ListContentResources fetches the CAS artifact catalog for this tenant's CPI subaccount.
// Returns only entries; caller filters by SubType == "package".
// The catalog is the authoritative source for artifact GUIDs and package metadata
// required to build a valid ExportRequest.
func (c *CasClient) ListContentResources(ctx context.Context) ([]CatalogContentResource, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.LongRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/contentResources", c.ApiURL)
	req := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}

	body, statusCode, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("ListContentResources: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("ListContentResources: unexpected status %d: %s", statusCode, safeBody(body))
	}

	var resp struct {
		ContentResources []CatalogContentResource `json:"contentResources"`
	}
	if err := json.Unmarshal(*body, &resp); err != nil {
		return nil, fmt.Errorf("ListContentResources: unmarshal: %w", err)
	}
	return resp.ContentResources, nil
}

// TriggerExport calls POST /v1/contentResources/export and returns the
// ExportResponse containing the processId for subsequent polling.
func (c *CasClient) TriggerExport(ctx context.Context, req ExportRequest) (*ExportResponse, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("TriggerExport: marshal request: %w", err)
	}

	fullURL := fmt.Sprintf("%s/v1/contentResources/export", c.ApiURL)
	httpReq := &env.HttpRequest{
		ApiURL:      fullURL,
		Method:      http.MethodPost,
		RequestBody: bytes.NewBuffer(payload),
	}

	body, statusCode, err := c.Do(childCtx, httpReq)
	if err != nil {
		return nil, fmt.Errorf("TriggerExport: %w", err)
	}
	if statusCode != http.StatusOK && statusCode != http.StatusAccepted {
		return nil, fmt.Errorf("TriggerExport: unexpected status %d: %s", statusCode, safeBody(body))
	}

	var resp ExportResponse
	if err := json.Unmarshal(*body, &resp); err != nil {
		return nil, fmt.Errorf("TriggerExport: unmarshal: %w", err)
	}
	if resp.ProcessID == "" {
		return nil, fmt.Errorf("TriggerExport: response missing processId: %s", safeBody(body))
	}
	logger().Infow("CAS export triggered", "processId", resp.ProcessID, "activityId", resp.ActivityID)
	return &resp, nil
}

// PollOperation calls GET /v1/operations/{processId}?messages=true and
// returns the current OperationStatus.  The caller loops until FINISHED or FAILED.
func (c *CasClient) PollOperation(ctx context.Context, processID string) (*OperationStatus, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/operations/%s?messages=true", c.ApiURL, processID)
	req := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}

	body, statusCode, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("PollOperation(%s): %w", processID, err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("PollOperation(%s): unexpected status %d: %s", processID, statusCode, safeBody(body))
	}

	var status OperationStatus
	if err := json.Unmarshal(*body, &status); err != nil {
		return nil, fmt.Errorf("PollOperation(%s): unmarshal: %w", processID, err)
	}
	return &status, nil
}

// GetOperationConfig calls GET /v1/operations/{processId}/config?logs=true.
// Must only be called after PollOperation returns State = "FINISHED".
// TransportRequestID in the response is the authoritative TR ID.
func (c *CasClient) GetOperationConfig(ctx context.Context, processID string) (*OperationConfig, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/operations/%s/config?logs=true", c.ApiURL, processID)
	req := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}

	body, statusCode, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("GetOperationConfig(%s): %w", processID, err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("GetOperationConfig(%s): unexpected status %d: %s", processID, statusCode, safeBody(body))
	}

	var cfg OperationConfig
	if err := json.Unmarshal(*body, &cfg); err != nil {
		return nil, fmt.Errorf("GetOperationConfig(%s): unmarshal: %w", processID, err)
	}
	return &cfg, nil
}

// GetActivities calls GET /v1/activities with requestor filter.
// Auxiliary audit path; not the primary TR ID source.
// top limits the result count (0 = server default).
func (c *CasClient) GetActivities(ctx context.Context, requestor string, top int) ([]Activity, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/activities?filters=requestor eq '%s'", c.ApiURL, requestor)
	if top > 0 {
		fullURL = fmt.Sprintf("%s&top=%d", fullURL, top)
	}
	req := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}

	body, statusCode, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("GetActivities: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("GetActivities: unexpected status %d: %s", statusCode, safeBody(body))
	}

	var resp struct {
		Activities []Activity `json:"activities"`
	}
	if err := json.Unmarshal(*body, &resp); err != nil {
		return nil, fmt.Errorf("GetActivities: unmarshal: %w", err)
	}
	return resp.Activities, nil
}

// safeBody converts a response body pointer to a truncated string for error messages.
func safeBody(b *[]byte) string {
	if b == nil {
		return "<nil>"
	}
	s := string(*b)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
