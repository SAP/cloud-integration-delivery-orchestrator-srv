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

// logger returns the package logger, resolved lazily via env.Logger().
func logger() *zap.SugaredLogger { return env.Logger() }

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
}

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
	return &CpiClient{client}, err
}

func (c *CpiClient) GetPackages(ctx context.Context) ([]CPIPackage, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.ApiURL)
	logger().Infof("Starting to get all packages from cpi tenant %s\n", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}

	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetPackages request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return []CPIPackage{}, errReq
	}

	var packcageResp PackagesResponse
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []CPIPackage{}, jsonUnmarshalError
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
	logger().Infof("Starting to get packages %s from cpi tenant %s\n", packageID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetPackage request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return CPIPackage{}, errReq
	}
	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return CPIPackage{}, jsonUnmarshalError
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
	logger().Infof("Starting to import package to cpi tenant %s\n", fullURL)
	requestBodyJson, _ := json.Marshal(cpiPackage)
	request := env.HttpRequest{
		Method:      http.MethodPost,
		ApiURL:      fullURL,
		RequestBody: bytes.NewBuffer(requestBodyJson),
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("ImportPackage request timeout after %v: %s", consts.ImportTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return CPIPackage{}, errReq
	}

	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return CPIPackage{}, jsonUnmarshalError
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
	logger().Infof("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, statusCode, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetPackageIflows request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return []IflowItem{}, errReq
	}
	if statusCode != 200 {
		bodyPreview := string(*respBodyContent)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500]
		}
		logger().Errorf("[DEBUG] GetPackageIflows non-200 response for package %q: status=%d, url=%s, body=%s", packageID, statusCode, fullURL, bodyPreview)
		return []IflowItem{}, fmt.Errorf("CPI API returned status %d for package %q", statusCode, packageID)
	}
	var iflowsResp PackageIflowsResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &iflowsResp)

	if jsonUnmarshalError != nil {
		bodyPreview := string(*respBodyContent)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500]
		}
		logger().Errorf("[DEBUG] GetPackageIflows JSON unmarshal failed for package %q: err=%s, body=%s", packageID, jsonUnmarshalError, bodyPreview)
		return []IflowItem{}, jsonUnmarshalError
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
	logger().Infof("Starting to get iflow %s in package %s from cpi tenant %s\n", iflowID, packageID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetPackageIflow request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content: %s", errReq)
		return IflowItem{}, errReq
	}
	var iflowResp IflowResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &iflowResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return IflowItem{}, jsonUnmarshalError
	}

	if iflowResp.D.ID == "" {
		return IflowItem{}, fmt.Errorf("design time iflow %s:%s not found: %s", iflowID, iflowVersion, string(*respBodyContent))
	}

	return iflowResp.D, nil
}

// Get a design time integration flow by Id and version.
func (c *CpiClient) GetDesignTimeIflow(ctx context.Context, iflowID string, iflowVersion string) (IflowItem, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, iflowID, iflowVersion)

	logger().Infof("Starting to get iflow %s from cpi tenant %s\n", iflowID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetDesignTimeIflow request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return IflowItem{}, errReq
	}
	var iflowResp IflowResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &iflowResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return IflowItem{}, jsonUnmarshalError
	}

	return iflowResp.D, nil
}

// Deploy a design time integration flow
func (c *CpiClient) DeployIflow(ctx context.Context, iflowID string, iflowVersion string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployIntegrationDesigntimeArtifact?Id='%s'&Version='%s'", c.ApiURL, iflowID, iflowVersion)
	logger().Infof("Starting to deploy iflow %s on tenant %s\n", iflowID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodPost,
		ApiURL: fullURL,
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("DeployIflow request timeout after %v: %s", consts.ImportTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return taskID, errReq
	}
	taskID = string(*respBodyContent)
	return taskID, nil
}

// deploy a design time script collection
func (c *CpiClient) DeployScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployScriptCollectionDesigntimeArtifact?Id='%s'&Version='%s'", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger().Infof("Starting to deploy script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodPost,
		ApiURL: fullURL,
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("DeployScriptCollection request timeout after %v: %s", consts.ImportTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return "", errReq
	}
	taskID = string(*respBodyContent)
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
	logger().Infof("Checking the deploy status for task id  %s on tenant %s\n", taskID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("CheckDeployStatusByTaskID request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return "", fmt.Errorf("error when getting response from %s: %s", fullURL, errReq)
	}
	var deployStatus DeployStatus
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &deployStatus)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return "", jsonUnmarshalError
	}
	return deployStatus.D.Status, nil
}

func (c *CpiClient) DeleteIflow(ctx context.Context, iflowID string, iflowVersion string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, iflowID, iflowVersion)
	logger().Infof("Starting to delete iflow %s on tenant %s\n", iflowID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	_, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("DeleteIflow request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return errReq
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
	logger().Infof("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, statusCode, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetPackageScriptcollections request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return []ScriptCollectionItem{}, errReq
	}
	if statusCode != 200 {
		bodyPreview := string(*respBodyContent)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500]
		}
		logger().Errorf("[DEBUG] GetPackageScriptcollections non-200 response for package %q: status=%d, url=%s, body=%s", packageID, statusCode, fullURL, bodyPreview)
		return []ScriptCollectionItem{}, fmt.Errorf("CPI API returned status %d for package %q", statusCode, packageID)
	}
	var scriptCollectionsResp ScriptCollectionsResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &scriptCollectionsResp)

	if jsonUnmarshalError != nil {
		bodyPreview := string(*respBodyContent)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500]
		}
		logger().Errorf("[DEBUG] GetPackageScriptcollections JSON unmarshal failed for package %q: err=%s, body=%s", packageID, jsonUnmarshalError, bodyPreview)
		return []ScriptCollectionItem{}, jsonUnmarshalError
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
	logger().Infof("Starting to get script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("GetDesignTimeScriptCollection request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return ScriptCollectionItem{}, errReq
	}
	var scriptCollectionResp ScriptCollectionResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &scriptCollectionResp)

	if jsonUnmarshalError != nil {
		logger().Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return ScriptCollectionItem{}, jsonUnmarshalError
	}

	if scriptCollectionResp.D.ID == "" {
		return ScriptCollectionItem{}, fmt.Errorf("design time script collection %s:%s not found: %s", scriptCollectionID, scriptCollectionVersion, string(*respBodyContent))
	}

	return scriptCollectionResp.D, nil
}

func (c *CpiClient) DeleteScriptCollection(ctx context.Context, scriptCollectionID string, scriptCollectionVersion string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger().Infof("Starting to delete script collection %s on tenant %s\n", scriptCollectionID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	_, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("DeleteScriptCollection request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return errReq
	}
	return nil

}

func (c *CpiClient) UndeployRuntimeArtifacts(ctx context.Context, artifactID string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactID)
	logger().Infof("Starting to undeploy artifact %s on tenant %s\n", artifactID, fullURL)
	request := env.HttpRequest{
		Method: http.MethodDelete,
		ApiURL: fullURL,
	}
	_, _, errReq := c.Do(childCtx, &request)
	if errReq != nil {
		if errors.Is(errReq, context.DeadlineExceeded) {
			logger().Errorf("UndeployRuntimeArtifacts request timeout after %v: %s", consts.ImportTimeout, fullURL)
		}
		logger().Errorf("Error when getting response content, the error message is %s", errReq)
		return errReq
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
	logger().Infof("Starting to Get all deployed integration artifacts from cpi tenant %s\n", fullURL)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	response, _, err := c.Do(childCtx, &request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger().Errorf("GetRuntimeArtifacts request timeout after %v: %s", consts.DefaultRequestTimeout, fullURL)
		}
		return nil, err
	}
	var runtimeArtifactsResp RuntimeArtifactsResp
	if err := json.Unmarshal(*response, &runtimeArtifactsResp); err != nil {
		return nil, err
	}
	return runtimeArtifactsResp.D.Results, nil
}

// Check the undeploy status of a runtime artifact, i.e., check if a artifact id exists in runtime.
// If not, means the artifact has been undeployed successfully
func (c *CpiClient) CheckUndeployStatus(ctx context.Context, artifactId string) (string, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullUrl := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactId)
	logger().Infof("Starting to check undeploy status of artifact %s on tenant %s\n", artifactId, fullUrl)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullUrl,
	}
	var response *[]byte
	var err error
	if response, _, err = c.Do(childCtx, &request); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger().Errorf("CheckUndeployStatus request timeout after %v: %s", consts.DefaultRequestTimeout, fullUrl)
		}
		return "", fmt.Errorf("failed to get undeploy stauts from %s: %s", fullUrl, err)
	}
	var runtimeArtifact RuntimeArtifact
	if err := json.Unmarshal(*response, &runtimeArtifact); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %s", err)
	}
	if runtimeArtifact.ID == "" {
		logger().Infof("Artifact %s has been successfully undeployed", artifactId)
		return "SUCCESS", nil
	}
	logger().Warnf("artifact %s is still in runtime", artifactId)
	return "UNDEPLOYING", nil
}

// Get a runtime artifact by Id.
// status: STARTED, ERROR, STARTING(not sure)
func (c *CpiClient) RuntimeArtifact(ctx context.Context, artifactId string) (RuntimeArtifact, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullUrl := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactId)
	logger().Infof("Starting to get runtime artifact %s on tenant %s\n", artifactId, fullUrl)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullUrl,
	}
	response, code, err := c.Do(childCtx, &request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger().Errorf("RuntimeArtifact request timeout after %v: %s", consts.DefaultRequestTimeout, fullUrl)
		}
		return RuntimeArtifact{}, fmt.Errorf("failed to get runtime artifact from %s: %s", fullUrl, err)
	}
	if code == http.StatusNotFound {
		return RuntimeArtifact{}, fmt.Errorf("runtime artifact %s not found", artifactId)
	}
	var t struct {
		D RuntimeArtifact `json:"d"`
	}
	if err := json.Unmarshal(*response, &t); err != nil {
		return RuntimeArtifact{}, fmt.Errorf("failed to unmarshal response: %s", err)
	}
	return t.D, nil
}
