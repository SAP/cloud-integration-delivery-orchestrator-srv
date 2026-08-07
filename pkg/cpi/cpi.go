package cpi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"mmt-delivery/consts"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"

	"go.uber.org/zap"
)

// logger returns a context-aware logger that includes trace_id/span_id when OTel is active.
func logger(ctx context.Context) *zap.SugaredLogger { return env.L(ctx) }

type CPIPackage struct {
	ID                string `json:"Id"`
	Name              string `json:"Name"`
	Description       string `json:"Description"`
	ShortText         string `json:"ShortText"`
	Version           string `json:"Version"`
	Vendor            string `json:"Vendor"`
	Mode              string `json:"Mode"`
	SupportedPlatform string `json:"SupportedPlatform"`
	ModifiedBy        string `json:"ModifiedBy"`
	CreationDate      string `json:"CreationDate"`
	ModifiedDate      string `json:"ModifiedDate"`
	CreatedBy         string `json:"CreatedBy"`
	Products          string `json:"Products"`
	Keywords          string `json:"Keywords"`
	Countries         string `json:"Countries"`
	Industries        string `json:"Industries"`
	LineOfBusiness    string `json:"LineOfBusiness"`
}

type PackagesResponse struct {
	D struct {
		Results []CPIPackage `json:"results"`
	} `json:"d"`
}

type CpiClient struct {
	*env.HttpClient
	sem chan struct{} // limits concurrent requests to this CPI tenant
}

// maxConcurrentRequests limits parallel outgoing requests per CPI tenant to avoid 429.
const maxConcurrentRequests = 10

func NewClient(ctx context.Context, destinationName string, resolver *cf.DestinationServiceClient) (*CpiClient, error) {
	cpiDest, err := resolver.GetDestination(ctx, destinationName)
	if err != nil {
		return nil, fmt.Errorf("CPI destination(for it-tr/api) %s not found: %w", destinationName, err)
	}
	// Normalise base URL: strip any trailing /api/v1, /v1, or / that may have been
	// included in the destination configuration, then always append /api/v1.
	base := strings.TrimRight(cpiDest.URL, "/")
	for _, suffix := range []string{"/api/v1", "/v1"} {
		if trimmed, ok := strings.CutSuffix(base, suffix); ok {
			base = trimmed
			break
		}
	}
	apiUrl := base + "/api/v1"
	client, err := env.NewClient(ctx, cpiDest.ClientId, cpiDest.ClientSecret, cpiDest.TokenServiceURL, apiUrl)
	return &CpiClient{HttpClient: client, sem: make(chan struct{}, maxConcurrentRequests)}, err
}

// Do wraps HttpClient.Do with per-tenant concurrency limiting.
func (c *CpiClient) Do(ctx context.Context, request *env.HttpRequest) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return c.HttpClient.Do(ctx, request)
}

func (c *CpiClient) GetPackages(ctx context.Context) ([]CPIPackage, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.ApiURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}

	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return []CPIPackage{}, fmt.Errorf("GetPackages: %w", errReq)
	}

	var packcageResp PackagesResponse
	if err := json.Unmarshal(respBodyContent, &packcageResp); err != nil {
		return []CPIPackage{}, fmt.Errorf("GetPackages: unmarshal: %w", err)
	}

	return packcageResp.D.Results, nil
}

type PackageResponse struct {
	D CPIPackage `json:"d"`
}

func (c *CpiClient) GetPackage(ctx context.Context, packageID string) (CPIPackage, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')", c.ApiURL, packageID)
	logger(ctx).Infow("get package", "package_id", packageID)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return CPIPackage{}, fmt.Errorf("GetPackage: %w", errReq)
	}
	var packcageResp PackageResponse
	if err := json.Unmarshal(respBodyContent, &packcageResp); err != nil {
		return CPIPackage{}, fmt.Errorf("GetPackage: unmarshal: %w", err)
	}

	return packcageResp.D, nil
}

type importPackageRequest struct {
	ID                string `json:"Id"`
	Name              string `json:"Name"`
	Description       string `json:"Description"`
	ShortText         string `json:"ShortText"`
	Version           string `json:"Version"`
	SupportedPlatform string `json:"SupportedPlatform"`
	Products          string `json:"Products"`
	Keywords          string `json:"Keywords"`
	Countries         string `json:"Countries"`
	Industries        string `json:"Industries"`
	LineOfBusiness    string `json:"LineOfBusiness"`
}

func (c *CpiClient) ImportPackage(ctx context.Context, cpiPackage importPackageRequest) (CPIPackage, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.ApiURL)
	requestBodyJson, _ := json.Marshal(cpiPackage)
	request := env.HttpRequest{
		Method:      http.MethodPost,
		ApiURL:      fullURL,
		RequestBody: requestBodyJson,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return CPIPackage{}, fmt.Errorf("ImportPackage: %w", errReq)
	}

	var packcageResp PackageResponse
	if err := json.Unmarshal(respBodyContent, &packcageResp); err != nil {
		return CPIPackage{}, fmt.Errorf("ImportPackage: unmarshal: %w", err)
	}
	return packcageResp.D, nil
}

type ArtifactCommonItem struct {
	ID              string      `json:"Id"`
	Version         string      `json:"Version"`
	PackageID       string      `json:"PackageId"`
	Name            string      `json:"Name"`
	Description     string      `json:"Description"`
	CreatedBy       string      `json:"CreatedBy"`
	CreatedAt       string      `json:"CreatedAt"`
	ModifiedBy      string      `json:"ModifiedBy"`
	ModifiedAt      string      `json:"ModifiedAt"`
	ArtifactContent interface{} `json:"ArtifactContent"`
}

type packageArtifactsResp struct {
	D struct {
		Results []ArtifactCommonItem `json:"results"`
	} `json:"d"`
}

type singleArtifactResp struct {
	D ArtifactCommonItem `json:"d"`
}

// GetPackageArtifactsByType queries all artifacts of a given type within a package.
// Uses: GET /api/v1/IntegrationPackages('{packageID}')/{NavProperty}
// Note: draft artifacts will have Version="Active" in the response.
func (c *CpiClient) GetPackageArtifactsByType(ctx context.Context, packageID string, artifactType consts.ArtifactType) ([]ArtifactCommonItem, error) {
	navProperty, ok := consts.NavProperty(artifactType)
	if !ok {
		return nil, fmt.Errorf("GetPackageArtifactsByType: unsupported artifact type: %s", artifactType)
	}
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/%s", c.ApiURL, packageID, navProperty)
	logger(ctx).Infow("get package artifacts", "package_id", packageID, "type", artifactType)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return nil, fmt.Errorf("GetPackageArtifactsByType(%s/%s): %w", packageID, navProperty, errReq)
	}
	var resp packageArtifactsResp
	if err := json.Unmarshal(respBodyContent, &resp); err != nil {
		return nil, fmt.Errorf("GetPackageArtifactsByType(%s/%s): unmarshal: %w", packageID, navProperty, err)
	}
	return resp.D.Results, nil
}

// GetDesignTimeArtifact queries a single artifact by ID and version via Direct API.
// Uses: GET /api/v1/{NavProperty}(Id='{artifactID}',Version='{version}')
// When version="active", the response contains the actual formal version number.
func (c *CpiClient) GetDesignTimeArtifact(ctx context.Context, artifactID, version string, artifactType consts.ArtifactType) (ArtifactCommonItem, error) {
	navProperty, ok := consts.NavProperty(artifactType)
	if !ok {
		return ArtifactCommonItem{}, fmt.Errorf("GetDesignTimeArtifact: unsupported artifact type: %s", artifactType)
	}
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/%s(Id='%s',Version='%s')", c.ApiURL, navProperty, artifactID, version)
	logger(ctx).Infow("get design time artifact", "artifact_id", artifactID, "type", artifactType)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return ArtifactCommonItem{}, fmt.Errorf("GetDesignTimeArtifact(%s:%s): %w", artifactID, version, errReq)
	}
	var resp singleArtifactResp
	if err := json.Unmarshal(respBodyContent, &resp); err != nil {
		return ArtifactCommonItem{}, fmt.Errorf("GetDesignTimeArtifact(%s:%s): unmarshal: %w", artifactID, version, err)
	}
	if resp.D.ID == "" {
		return ArtifactCommonItem{}, fmt.Errorf("design time artifact %s:%s not found: %s", artifactID, version, string(respBodyContent))
	}
	return resp.D, nil
}

// DeployArtifact deploys a design-time artifact via the type-specific deploy endpoint.
// Returns taskID for checking deploy status. Returns error if the type is not deployable.
func (c *CpiClient) DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error) {
	endpoint, ok := consts.DeployEndpoint(artifactType)
	if !ok {
		return "", fmt.Errorf("artifact type %s is not deployable (artifact %s:%s)", artifactType, artifactID, artifactVersion)
	}
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/%s?Id='%s'&Version='%s'", c.ApiURL, endpoint, artifactID, artifactVersion)
	logger(ctx).Infow("deploy artifact", "artifact_id", artifactID, "type", artifactType)
	request := env.HttpRequest{
		Method: http.MethodPost,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return "", fmt.Errorf("DeployArtifact(%s:%s): %w", artifactID, artifactVersion, errReq)
	}
	return string(respBodyContent), nil
}

type DeployStatus struct {
	D struct {
		Metadata struct {
			ID   string `json:"id"`
			URI  string `json:"uri"`
			Type string `json:"type"`
		} `json:"__metadata"`
		TaskID string `json:"TaskId"`
		Status string `json:"Status"`
	} `json:"d"`
}

// Success, Fail, Deploying, Fail_On_License_Error
// Note: this API is not stable, since occasionally it returns DEPLOYING though the artifact has been deployed successfully.
func (c *CpiClient) CheckDeployStatusByTaskID(ctx context.Context, taskID string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/BuildAndDeployStatus(TaskId='%s')", c.ApiURL, taskID)
	logger(ctx).Infow("check deploy status", "task_id", taskID)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return "", fmt.Errorf("CheckDeployStatusByTaskID: %w", errReq)
	}
	var deployStatus DeployStatus
	if err := json.Unmarshal(respBodyContent, &deployStatus); err != nil {
		return "", fmt.Errorf("CheckDeployStatusByTaskID: unmarshal: %w", err)
	}
	return deployStatus.D.Status, nil
}

func (c *CpiClient) UndeployRuntimeArtifacts(ctx context.Context, artifactID string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactID)
	logger(ctx).Infow("undeploy runtime artifact", "artifact_id", artifactID)
	request := env.HttpRequest{
		Method: http.MethodDelete,
		ApiURL: fullURL,
	}
	_, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return fmt.Errorf("UndeployRuntimeArtifacts: %w", errReq)
	}
	return nil
}

type RuntimeArtifactsResp struct {
	D struct {
		Results []RuntimeArtifact `json:"results"`
	} `json:"d"`
}

type RuntimeArtifact struct {
	ID         string              `json:"Id"`
	Version    string              `json:"Version"`
	Name       string              `json:"Name"`
	Type       string              `json:"Type"`
	DeployedBy string              `json:"DeployedBy"`
	DeployedOn string              `json:"DeployedOn"`
	Status     consts.RuntimeState `json:"Status"`
}

// Get all deployed(runtime) artifacts
func (c *CpiClient) GetRuntimeArtifacts(ctx context.Context) ([]RuntimeArtifact, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationRuntimeArtifacts", c.ApiURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	response, err := c.Do(childCtx, &request)
	if err != nil {
		return nil, fmt.Errorf("GetRuntimeArtifacts: %w", err)
	}
	var runtimeArtifactsResp RuntimeArtifactsResp
	if err := json.Unmarshal(response, &runtimeArtifactsResp); err != nil {
		return nil, fmt.Errorf("GetRuntimeArtifacts: unmarshal: %w", err)
	}
	return runtimeArtifactsResp.D.Results, nil
}

// Check the undeploy status of a runtime artifact, i.e., check if a artifact id exists in runtime.
// If not, means the artifact has been undeployed successfully
func (c *CpiClient) CheckUndeployStatus(ctx context.Context, artifactId string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullUrl := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactId)
	logger(ctx).Infow("check undeploy status", "artifact_id", artifactId)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullUrl,
	}
	var response []byte
	var err error
	if response, err = c.Do(childCtx, &request); err != nil {
		return "", fmt.Errorf("CheckUndeployStatus: %w", err)
	}
	var runtimeArtifact RuntimeArtifact
	if err := json.Unmarshal(response, &runtimeArtifact); err != nil {
		return "", fmt.Errorf("CheckUndeployStatus: unmarshal: %w", err)
	}
	if runtimeArtifact.ID == "" {
		logger(ctx).Infow("artifact undeployed successfully", "artifact_id", artifactId)
		return "SUCCESS", nil
	}
	logger(ctx).Warnw("artifact still in runtime", "artifact_id", artifactId)
	return "UNDEPLOYING", nil
}

// Get a runtime artifact by Id.
// status: STARTED, ERROR, STARTING(not sure)
func (c *CpiClient) RuntimeArtifact(ctx context.Context, artifactId string) (RuntimeArtifact, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullUrl := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactId)
	logger(ctx).Infow("get runtime artifact", "artifact_id", artifactId)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullUrl,
	}
	response, err := c.Do(childCtx, &request)
	if err != nil {
		var httpErr *env.HttpResponseError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return RuntimeArtifact{}, fmt.Errorf("runtime artifact %s not found", artifactId)
		}
		return RuntimeArtifact{}, fmt.Errorf("RuntimeArtifact: %w", err)
	}
	var t struct {
		D RuntimeArtifact `json:"d"`
	}
	if err := json.Unmarshal(response, &t); err != nil {
		return RuntimeArtifact{}, fmt.Errorf("RuntimeArtifact: unmarshal: %w", err)
	}
	return t.D, nil
}

// DownloadArtifactZip downloads the artifact content as a ZIP file from CPI design-time API.
// Uses: GET /api/v1/{NavProperty}(Id='{artifactID}',Version='{version}')/$value
func (c *CpiClient) DownloadArtifactZip(ctx context.Context, artifactID, version string, artifactType consts.ArtifactType) ([]byte, error) {
	navProperty, ok := consts.NavProperty(artifactType)
	if !ok {
		return nil, fmt.Errorf("DownloadArtifactZip: unsupported artifact type %s", artifactType)
	}
	childCtx, cancel := context.WithTimeout(ctx, consts.LongRequestTimeout)
	defer cancel()

	endpoint := fmt.Sprintf("%s/%s(Id='%s',Version='%s')/$value", c.ApiURL, navProperty, artifactID, version)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: endpoint,
	}
	zipBytes, err := c.Do(childCtx, &request)
	if err != nil {
		return nil, fmt.Errorf("DownloadArtifactZip(%s:%s): %w", artifactID, version, err)
	}
	return zipBytes, nil
}
