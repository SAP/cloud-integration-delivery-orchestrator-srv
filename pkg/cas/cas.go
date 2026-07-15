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
	"net/url"

	"mmt-delivery/consts"
	"mmt-delivery/pkg/env"

	"go.uber.org/zap"
)

func logger(ctx context.Context) *zap.SugaredLogger { return env.L(ctx) }

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
	ID           string             `json:"id"`         // package tech ID (e.g. "AlexTest")
	ResourceID   string             `json:"resourceID"` // package GUID (e.g. "d8e90a3b...")
	Type         string             `json:"type"`       // "Cloud Integration"
	Name         string             `json:"name"`       // package display name
	SubType      string             `json:"subType"`    // "package" | "Destination"
	Version      string             `json:"version"`    // package version
	Components   []CatalogComponent `json:"components"`
	Dependencies []any              `json:"dependencies"`
}

// CatalogComponent is one artifact entry within a CatalogContentResource.
// component.name is the artifact display name — matches Artifact.Name in our DB.
// component.id is the artifact GUID required by ExportRequest.
// CAS does NOT return the artifact tech ID (CPI OData "Id" field).
type CatalogComponent struct {
	ID         string `json:"id"`      // artifact GUID
	Name       string `json:"name"`    // artifact display name — matches Artifact.Name in our DB
	Type       string `json:"type"`    // "Integration Flow" | "Script Collection" | "OData Service" | "Integration Adapter" | …
	Version    string `json:"version"` // current version in CAS
	Exportable bool   `json:"exportable"`
}

// ── Export request/response types ─────────────────────────────────────────────

// ExportRequest is the body for POST /v1/contentResources/export.
// The top-level "id" is a required string that the CAS API uses as a request
// correlation key — it must be non-empty. We use the DR ID formatted as a string.
type ExportRequest struct {
	ID                   string            `json:"id"`              // required; use DR ID as string
	Requestor            string            `json:"requestor"`       // fixed "CPIDelivery"
	Version              string            `json:"version"`         // fixed "1.0.0"
	ExportMode           string            `json:"exportMode"`      // fixed "TransportManagementService"
	ExportMediaType      string            `json:"exportMediaType"` // fixed "MTAR"
	Description          string            `json:"description"`     // "DR#<ID> | <Name> <Version> | …"
	ContentResources     []ContentResource `json:"contentResources"`
	SourceNode           string            `json:"sourceNode"`           // CpiTenant.TmsSourceNodeName
	TransportDestination string            `json:"transportDestination"` // fixed "TransportManagementService"
	IsModifiable         bool              `json:"isModifiable"`         // fixed false
}

// ContentResource is one Cloud Integration package in an ExportRequest.
// Fields are populated from the CAS catalog (CatalogContentResource) to ensure
// correct GUIDs and package metadata.
type ContentResource struct {
	ID                    string                     `json:"id"`          // package tech ID
	ResourceID            string                     `json:"resourceID"`  // package GUID
	ContentType           string                     `json:"contentType"` // fixed "Cloud Integration"
	SubType               string                     `json:"subType"`     // fixed "package"
	Type                  string                     `json:"type"`        // fixed "Cloud Integration"
	Name                  string                     `json:"name"`
	Version               string                     `json:"version"` // package version from catalog
	Components            []ContentResourceComponent `json:"components"`
	Dependencies          []any                      `json:"dependencies"`          // fixed []
	MtaDescriptorSpecific MtaDescriptorSpecific      `json:"mtaDescriptorSpecific"` // fixed {"deployed-after":[]}
}

// MtaDescriptorSpecific is the fixed MTA metadata appended to each ContentResource.
type MtaDescriptorSpecific struct {
	DeployedAfter []any `json:"deployed-after"` // fixed []
}

// ContentResourceComponent is one artifact entry within a ContentResource.
// id must be the artifact GUID from the CAS catalog (not the tech ID).
type ContentResourceComponent struct {
	ID                   string `json:"id"`                   // artifact GUID from catalog
	Name                 string `json:"name"`                 // artifact tech ID / display name
	Type                 string `json:"type"`                 // "Integration Flow" | "Script Collection" | …
	Version              string `json:"version"`              // version from ArtifactTenantOperation
	Selected             bool   `json:"selected"`             // fixed true
	Enabled              bool   `json:"enabled"`              // fixed true
	Mandatory            bool   `json:"mandatory"`            // fixed false
	DefaultSelect        bool   `json:"defaultSelect"`        // fixed false
	AdditionalProperties any    `json:"additionalProperties"` // fixed null
	Exportable           bool   `json:"exportable"`           // from catalog
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
	State       string             `json:"state"`    // INITIAL / STARTED / RUNNING / FINISHED / FAILED
	Progress    int                `json:"progress"` // 0–100
	Messages    []OperationMessage `json:"messages,omitempty"`
}

// OperationMessage is a single log entry within OperationStatus.Messages.
type OperationMessage struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Type      string `json:"type"` // INFO / ERROR / WARNING
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

// ListCloudIntegrationResources fetches CAS artifact catalog entries for Cloud Integration only.
// packageIDs optionally restricts results to specific package tech IDs; filtering is done
// client-side after fetching because the CAS API does not support combining type and id filters.
// Pass nil or empty slice to fetch the full Cloud Integration catalog.
// Caller should further filter by SubType == "package" when needed.
func (c *CasClient) ListCloudIntegrationResources(ctx context.Context, packageIDs []string) ([]CatalogContentResource, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.LongRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/contentResources", c.ApiURL)
	params := url.Values{}
	// Restrict to Cloud Integration only; CAS also returns API Management, Destination Service, etc.
	params.Set("filters", "type eq 'Cloud Integration'")
	fullURL += "?" + params.Encode()
	req := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}

	body, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("ListCloudIntegrationResources: %w", err)
	}

	var resp struct {
		ContentResources []CatalogContentResource `json:"contentResources"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("ListCloudIntegrationResources: unmarshal: %w", err)
	}

	// Client-side package filter (CAS API does not support combining type + id filters).
	if len(packageIDs) == 0 {
		return resp.ContentResources, nil
	}
	wanted := make(map[string]struct{}, len(packageIDs))
	for _, id := range packageIDs {
		wanted[id] = struct{}{}
	}
	filtered := make([]CatalogContentResource, 0, len(packageIDs))
	for _, r := range resp.ContentResources {
		if _, ok := wanted[r.ID]; ok {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
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

	body, err := c.Do(childCtx, httpReq)
	if err != nil {
		return nil, fmt.Errorf("TriggerExport: %w", err)
	}

	var resp ExportResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("TriggerExport: unmarshal: %w", err)
	}
	if resp.ProcessID == "" {
		return nil, fmt.Errorf("TriggerExport: response missing processId: %s", string(body))
	}
	logger(ctx).Infow("CAS export triggered", "processId", resp.ProcessID, "activityId", resp.ActivityID)
	return &resp, nil
}

// PollOperation calls GET /v1/operations/{processId}?messages=true and
// returns the current OperationStatus.  The caller loops until FINISHED or FAILED.
func (c *CasClient) PollOperation(ctx context.Context, processID string) (*OperationStatus, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()

	fullURL := fmt.Sprintf("%s/v1/operations/%s?messages=true", c.ApiURL, processID)
	req := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}

	body, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("PollOperation(%s): %w", processID, err)
	}

	var status OperationStatus
	if err := json.Unmarshal(body, &status); err != nil {
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

	body, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("GetOperationConfig(%s): %w", processID, err)
	}

	var cfg OperationConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
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

	fullURL := fmt.Sprintf("%s/v1/activities", c.ApiURL)
	params := url.Values{}
	params.Set("filters", fmt.Sprintf("requestor eq '%s'", requestor))
	if top > 0 {
		params.Set("top", fmt.Sprintf("%d", top))
	}
	fullURL += "?" + params.Encode()
	req := &env.HttpRequest{ApiURL: fullURL, Method: http.MethodGet}

	body, err := c.Do(childCtx, req)
	if err != nil {
		return nil, fmt.Errorf("GetActivities: %w", err)
	}

	var resp struct {
		Activities []Activity `json:"activities"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("GetActivities: unmarshal: %w", err)
	}
	return resp.Activities, nil
}
