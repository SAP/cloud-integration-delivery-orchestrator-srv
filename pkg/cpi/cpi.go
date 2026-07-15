package cpi

import (
	"bytes"
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
const maxConcurrentRequests = 5

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
	logger(ctx).Infow("get packages", "url", fullURL)
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
	logger(ctx).Infow("get package", "package_id", packageID, "url", fullURL)
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
	logger(ctx).Infow("import package", "url", fullURL)
	requestBodyJson, _ := json.Marshal(cpiPackage)
	request := env.HttpRequest{
		Method:      http.MethodPost,
		ApiURL:      fullURL,
		RequestBody: bytes.NewBuffer(requestBodyJson),
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
type IflowItem struct {
	ArtifactCommonItem
	Configurations struct {
		Deferred struct {
			URI string `json:"uri"`
		} `json:"__deferred"`
	} `json:"Configurations"`
	Resources struct {
		Deferred struct {
			URI string `json:"uri"`
		} `json:"__deferred"`
	} `json:"Resources"`
	Metadata struct {
		ID          string `json:"id"`
		URI         string `json:"uri"`
		Type        string `json:"type"`
		ContentType string `json:"content_type"`
		MediaSrc    string `json:"media_src"`
		EditMedia   string `json:"edit_media"`
	} `json:"__metadata"`
}

type PackageIflowsResp struct {
	D struct {
		Results []IflowItem `json:"results"`
	} `json:"d"`
}

// get all iflows in a package
func (c *CpiClient) GetPackageIflows(ctx context.Context, packageID string) ([]IflowItem, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts", c.ApiURL, packageID)
	logger(ctx).Infow("get package iflows", "package_id", packageID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return []IflowItem{}, fmt.Errorf("GetPackageIflows: %w", errReq)
	}
	var iflowsResp PackageIflowsResp
	if err := json.Unmarshal(respBodyContent, &iflowsResp); err != nil {
		return []IflowItem{}, fmt.Errorf("GetPackageIflows: unmarshal: %w", err)
	}

	return iflowsResp.D.Results, nil
}

type IflowResp struct {
	D IflowItem `json:"d"`
}

// Get an integration flow by Id and version.
func (c *CpiClient) GetPackageIflow(ctx context.Context, packageID string, iflowID string, iflowVersion string) (IflowItem, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, packageID, iflowID, iflowVersion)
	logger(ctx).Infow("get package iflow", "iflow_id", iflowID, "package_id", packageID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return IflowItem{}, fmt.Errorf("GetPackageIflow: %w", errReq)
	}
	var iflowResp IflowResp
	if err := json.Unmarshal(respBodyContent, &iflowResp); err != nil {
		return IflowItem{}, fmt.Errorf("GetPackageIflow: unmarshal: %w", err)
	}

	if iflowResp.D.ID == "" {
		return IflowItem{}, fmt.Errorf("design time iflow %s:%s not found: %s", iflowID, iflowVersion, string(respBodyContent))
	}

	return iflowResp.D, nil
}

// Get a design time integration flow by Id and version.
func (c *CpiClient) GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (IflowItem, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, iflowID, iflowVersion)

	logger(ctx).Infow("get design time iflow", "iflow_id", iflowID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return IflowItem{}, fmt.Errorf("GetDesignTimeIflow: %w", errReq)
	}
	var iflowResp IflowResp
	if err := json.Unmarshal(respBodyContent, &iflowResp); err != nil {
		return IflowItem{}, fmt.Errorf("GetDesignTimeIflow: unmarshal: %w", err)
	}

	return iflowResp.D, nil
}

// Deploy a design time integration flow
func (c *CpiClient) DeployIflow(ctx context.Context, iflowID string, iflowVersion string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployIntegrationDesigntimeArtifact?Id='%s'&Version='%s'", c.ApiURL, iflowID, iflowVersion)
	logger(ctx).Infow("deploy iflow", "iflow_id", iflowID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodPost,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return taskID, fmt.Errorf("DeployIflow: %w", errReq)
	}
	taskID = string(respBodyContent)
	return taskID, nil
}

// deploy a design time script collection
func (c *CpiClient) DeployScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployScriptCollectionDesigntimeArtifact?Id='%s'&Version='%s'", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger(ctx).Infow("deploy script collection", "script_collection_id", scriptCollectionID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodPost,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return "", fmt.Errorf("DeployScriptCollection: %w", errReq)
	}
	taskID = string(respBodyContent)
	return taskID, nil
}

// NOTE: currently only support iflow and script collection deployment
// return taskID for checking deploy status
func (c *CpiClient) DeployArtifact(ctx context.Context, artifactID, artifactVersion string, artifactType consts.ArtifactType) (string, error) {
	switch artifactType {
	case consts.Artifact_Type_Iflow:
		return c.DeployIflow(ctx, artifactID, artifactVersion)
	case consts.Artifact_Type_Sc:
		return c.DeployScriptCollection(ctx, artifactID, artifactVersion)
	}
	return "", fmt.Errorf("unsupported artifact type %s for artifact %s:%s", artifactType, artifactID, artifactVersion)
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
	logger(ctx).Infow("check deploy status", "task_id", taskID, "url", fullURL)
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

func (c *CpiClient) DeleteIflow(ctx context.Context, iflowID string, iflowVersion string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, iflowID, iflowVersion)
	logger(ctx).Infow("delete iflow", "iflow_id", iflowID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	_, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return fmt.Errorf("DeleteIflow: %w", errReq)
	}

	return nil

}

type ScriptCollectionItem struct {
	ArtifactCommonItem
	Metadata struct {
		ID          string `json:"id"`
		URI         string `json:"uri"`
		Type        string `json:"type"`
		ContentType string `json:"content_type"`
		MediaSrc    string `json:"media_src"`
		EditMedia   string `json:"edit_media"`
	} `json:"__metadata"`
}
type ScriptCollectionsResp struct {
	D struct {
		Results []ScriptCollectionItem `json:"results"`
	} `json:"d"`
}

// get all script collections under a package
func (c *CpiClient) GetPackageScriptcollections(ctx context.Context, packageID string) ([]ScriptCollectionItem, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/ScriptCollectionDesigntimeArtifacts", c.ApiURL, packageID)
	logger(ctx).Infow("get package script collections", "package_id", packageID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return []ScriptCollectionItem{}, fmt.Errorf("GetPackageScriptcollections: %w", errReq)
	}
	var scriptCollectionsResp ScriptCollectionsResp
	if err := json.Unmarshal(respBodyContent, &scriptCollectionsResp); err != nil {
		return []ScriptCollectionItem{}, fmt.Errorf("GetPackageScriptcollections: unmarshal: %w", err)
	}

	return scriptCollectionsResp.D.Results, nil
}

type ScriptCollectionResp struct {
	D ScriptCollectionItem `json:"d"`
}

// get a design time script collection
func (c *CpiClient) GetDesignTimeScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) (ScriptCollectionItem, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger(ctx).Infow("get design time script collection", "script_collection_id", scriptCollectionID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return ScriptCollectionItem{}, fmt.Errorf("GetDesignTimeScriptCollection: %w", errReq)
	}
	var scriptCollectionResp ScriptCollectionResp
	if err := json.Unmarshal(respBodyContent, &scriptCollectionResp); err != nil {
		return ScriptCollectionItem{}, fmt.Errorf("GetDesignTimeScriptCollection: unmarshal: %w", err)
	}

	if scriptCollectionResp.D.ID == "" {
		return ScriptCollectionItem{}, fmt.Errorf("design time script collection %s:%s not found: %s", scriptCollectionID, scriptCollectionVersion, string(respBodyContent))
	}

	return scriptCollectionResp.D, nil
}

func (c *CpiClient) DeleteScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger(ctx).Infow("delete script collection", "script_collection_id", scriptCollectionID, "url", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	_, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		return fmt.Errorf("DeleteScriptCollection: %w", errReq)
	}
	return nil

}

func (c *CpiClient) UndeployRuntimeArtifacts(ctx context.Context, artifactID string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactID)
	logger(ctx).Infow("undeploy runtime artifact", "artifact_id", artifactID, "url", fullURL)
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
	logger(ctx).Infow("get runtime artifacts", "url", fullURL)
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
	logger(ctx).Infow("check undeploy status", "artifact_id", artifactId, "url", fullUrl)
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
	logger(ctx).Infow("get runtime artifact", "artifact_id", artifactId, "url", fullUrl)
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
