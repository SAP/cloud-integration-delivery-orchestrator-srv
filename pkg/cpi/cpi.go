package cpi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.wdf.sap.corp/maco-mmt/maco-deploy/env"
)

var logger = env.Logger()

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
	env.HttpClient
}

func NewClient(ctx context.Context, tenant string) (*CpiClient, error) {
	cpiDest := env.Destinations()[tenant]
	apiUrl := fmt.Sprintf("%s/v1", cpiDest.URL)
	client, err := env.NewClient(ctx, cpiDest.ClientId, cpiDest.ClientSecret, cpiDest.TokenServiceURL, apiUrl)
	return &CpiClient{*client}, err
}

func (c *CpiClient) GetPackages() ([]CPIPackage, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.ApiURL)
	logger.Infof("Starting to get all packages from cpi tenant %s\n", fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}

	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []CPIPackage{}, errReq
	}

	var packcageResp PackagesResponse
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []CPIPackage{}, jsonUnmarshalError
	}

	return packcageResp.D.Results, nil
}

type PackageResponse struct {
	D CPIPackage `json:"d"`
}

func (c *CpiClient) GetPackage(packageID string) (CPIPackage, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')", c.ApiURL, packageID)
	logger.Infof("Starting to get packages %s from cpi tenant %s\n", packageID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return CPIPackage{}, errReq
	}
	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
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

func (c *CpiClient) ImportPackage(cpiPackage importPackageRequest) (CPIPackage, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.ApiURL)
	logger.Infof("Starting to import package to cpi tenant %s\n", fullURL)
	requestBodyJson, _ := json.Marshal(cpiPackage)
	request := env.HttpRequest{
		Ctx:         childCtx,
		Method:      http.MethodPost,
		ApiURL:      fullURL,
		RequestBody: bytes.NewBuffer(requestBodyJson),
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return CPIPackage{}, errReq
	}

	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return CPIPackage{}, jsonUnmarshalError
	}
	return packcageResp.D, nil
}

type IflowItem struct {
	ID              string      `json:"Id"`
	Version         string      `json:"Version"`
	PackageID       string      `json:"PackageId"`
	Name            string      `json:"Name"`
	Description     string      `json:"Description"`
	ArtifactContent interface{} `json:"ArtifactContent"`
	Configurations  struct {
		Deferred struct {
			URI string `json:"uri"`
		} `json:"__deferred"`
	} `json:"Configurations"`
	Resources struct {
		Deferred struct {
			URI string `json:"uri"`
		} `json:"__deferred"`
	} `json:"Resources"`
}

type PackageIflowsResp struct {
	D struct {
		Results []IflowItem `json:"results"`
	} `json:"d"`
}

// get all iflows in a package
func (c *CpiClient) GetPackageIflows(packageID string) ([]IflowItem, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts", c.ApiURL, packageID)
	logger.Infof("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []IflowItem{}, errReq
	}
	var iflowsResp PackageIflowsResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &iflowsResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return []IflowItem{}, jsonUnmarshalError
	}

	return iflowsResp.D.Results, nil
}

type IflowResp struct {
	D IflowItem `json:"d"`
}

// Get an integration flow by Id and version.
func (c *CpiClient) GetPackageIflow(packageID string, iflowID string, iflowVersion string) (IflowItem, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, packageID, iflowID, iflowVersion)
	logger.Infof("Starting to get iflow %s in package %s from cpi tenant %s\n", iflowID, packageID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response content: %s", errReq)
		return IflowItem{}, errReq
	}
	var iflowResp IflowResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &iflowResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return IflowItem{}, jsonUnmarshalError
	}

	if iflowResp.D.ID == "" {
		return IflowItem{}, fmt.Errorf("design time iflow %s:%s not found: %s", iflowID, iflowVersion, string(*respBodyContent))
	}

	return iflowResp.D, nil
}

func (c *CpiClient) GetIflow(iflowID string, iflowVersion string) (IflowItem, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, iflowID, iflowVersion)

	logger.Infof("Starting to get iflow %s from cpi tenant %s\n", iflowID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return IflowItem{}, errReq
	}
	var iflowResp IflowResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &iflowResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return IflowItem{}, jsonUnmarshalError
	}

	return iflowResp.D, nil
}

// Deploy a design time integration flow
func (c *CpiClient) DeployIflow(iflowID string, iflowVersion string) (string, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployIntegrationDesigntimeArtifact?Id='%s'&Version='%s'", c.ApiURL, iflowID, iflowVersion)
	logger.Infof("Starting to deploy iflow %s on tenant %s\n", iflowID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodPost,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return taskID, errReq
	}
	taskID = string(*respBodyContent)
	return taskID, nil
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
func (c *CpiClient) CheckDeployStatus(taskID string) (string, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/BuildAndDeployStatus(TaskId='%s')", c.ApiURL, taskID)
	logger.Infof("Checking the deploy status for task id  %s on tenant %s\n", taskID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response content, the error message is %s", errReq)
		return "", fmt.Errorf("error when getting response from %s: %s", fullURL, errReq)
	}
	var deployStatus DeployStatus
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &deployStatus)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return "", jsonUnmarshalError
	}
	return deployStatus.D.Status, nil
}

func (c *CpiClient) DeleteIflow(iflowID string, iflowVersion string) error {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, iflowID, iflowVersion)
	logger.Infof("Starting to delete iflow %s on tenant %s\n", iflowID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	_, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return errReq
	}

	return nil

}

type ScriptCollectionItem struct {
	ID              string `json:"Id"`
	Version         string `json:"Version"`
	PackageID       string `json:"PackageId"`
	Name            string `json:"Name"`
	Description     string `json:"Description"`
	ArtifactContent string `json:"ArtifactContent"`
}
type ScriptCollectionsResp struct {
	D struct {
		Results []ScriptCollectionItem `json:"results"`
	} `json:"d"`
}

// get all script collections under a package
func (c *CpiClient) GetPackageScriptcollections(packageID string) ([]ScriptCollectionItem, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/ScriptCollectionDesigntimeArtifacts", c.ApiURL, packageID)
	logger.Infof("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []ScriptCollectionItem{}, errReq
	}
	var scriptCollectionsResp ScriptCollectionsResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &scriptCollectionsResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return []ScriptCollectionItem{}, jsonUnmarshalError
	}

	return scriptCollectionsResp.D.Results, nil
}

type ScriptCollectionResp struct {
	D ScriptCollectionItem `json:"d"`
}

// get a design time script collection
func (c *CpiClient) GetScriptCollection(scriptCollectionID string, scriptCollectionVersion string) (ScriptCollectionItem, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger.Infof("Starting to get script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return ScriptCollectionItem{}, errReq
	}
	var scriptCollectionResp ScriptCollectionResp
	jsonUnmarshalError := json.Unmarshal(*respBodyContent, &scriptCollectionResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return ScriptCollectionItem{}, jsonUnmarshalError
	}

	if scriptCollectionResp.D.ID == "" {
		return ScriptCollectionItem{}, fmt.Errorf("design time script collection %s:%s not found: %s", scriptCollectionID, scriptCollectionVersion, string(*respBodyContent))
	}

	return scriptCollectionResp.D, nil
}

// deploy a design time script collection
func (c *CpiClient) DeployScriptCollection(scriptCollectionID string, scriptCollectionVersion string) (string, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployScriptCollectionDesigntimeArtifact?Id='%s'&Version='%s'", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger.Infof("Starting to deploy script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodPost,
		ApiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return "", errReq
	}
	taskID = string(*respBodyContent)
	return taskID, nil
}

func (c *CpiClient) DeleteScriptCollection(scriptCollectionID string, scriptCollectionVersion string) error {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.ApiURL, scriptCollectionID, scriptCollectionVersion)
	logger.Infof("Starting to delete script collection %s on tenant %s\n", scriptCollectionID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	_, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return errReq
	}
	return nil

}

func (c *CpiClient) UndeployRuntimeArtifacts(artifactID string) error {

	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactID)
	logger.Infof("Starting to undeploy artifact %s on tenant %s\n", artifactID, fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodDelete,
		ApiURL: fullURL,
	}
	_, errReq := c.Do(&request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
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
	ID         string `json:"Id"`
	Version    string `json:"Version"`
	Name       string `json:"Name"`
	Type       string `json:"Type"`
	DeployedBy string `json:"DeployedBy"`
	DeployedOn string `json:"DeployedOn"`
	Status     string `json:"Status"`
}

// Get all deployed(runtime) artifacts
func (c *CpiClient) GetRuntimeArtifacts() ([]RuntimeArtifact, error) {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/IntegrationRuntimeArtifacts", c.ApiURL)
	logger.Infof("Starting to Get all deployed integration artifacts from cpi tenant %s\n", fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	response, err := c.Do(&request)
	if err != nil {
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
func (c *CpiClient) CheckUndeployStatus(artifactId string) (string, error) {
	fullUrl := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.ApiURL, artifactId)
	logger.Infof("Starting to check undeploy status of artifact %s on tenant %s\n", artifactId, fullUrl)
	request := env.HttpRequest{
		Ctx:    c.Context,
		Method: http.MethodGet,
		ApiURL: fullUrl,
	}
	var response *[]byte
	var err error
	if response, err = c.Do(&request); err != nil {
		return "", fmt.Errorf("failed to get undeploy stauts from %s: %s", fullUrl, err)
	}
	var runtimeArtifact RuntimeArtifact
	if err := json.Unmarshal(*response, &runtimeArtifact); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %s", err)
	}
	if runtimeArtifact.ID == "" {
		logger.Infof("Artifact %s has been successfully undeployed", artifactId)
		return "SUCCESS", nil
	}
	logger.Warnf("artifact %s is still in runtime", artifactId)
	return "UNDEPLOYING", nil
}
