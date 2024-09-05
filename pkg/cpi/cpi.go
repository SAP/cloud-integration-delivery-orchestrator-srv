package cpi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
)

var logger = log.NewLogger().Sugar()

type CPIClient struct {
	context     context.Context
	HttpClient  *http.Client
	AccessToken string
	CpiApiURL   string
}

type OauthResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Jti         string `json:"jti"`
}

func NewCPIClient(ctx context.Context, clientID string, clientSecret string, cpiAuthURL string, cpiApiURL string) (*CPIClient, error) {
	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", clientID, clientSecret))

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cpiAuthURL, payload)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	httpClient := http.DefaultClient

	res, errReq := httpClient.Do(req)
	if errReq != nil {
		logger.Errorf("Error when get the response, %s", errReq)
		return &CPIClient{}, errReq
	}
	defer res.Body.Close()
	body, errIOReader := io.ReadAll(res.Body)
	if errIOReader != nil {
		logger.Errorf("Error when reading body from response, %s", errIOReader)
		return &CPIClient{}, errIOReader
	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		logger.Errorf("Error when extract json data from response, %s", jsonUnmarshalErr)
		return &CPIClient{}, jsonUnmarshalErr
	}
	return &CPIClient{
		context:     ctx,
		HttpClient:  httpClient,
		AccessToken: oauthResp.AccessToken,
		CpiApiURL:   cpiApiURL,
	}, nil
}

type clientRequest struct {
	ctx         context.Context
	apiURL      string
	method      string
	requestBody *bytes.Buffer
}

func (c *CPIClient) Do(request clientRequest) ([]byte, error) {
	childCtx, cancel := context.WithCancel(request.ctx)
	defer cancel()
	var req *http.Request
	if request.requestBody.String() == "<nil>" {
		req, _ = http.NewRequestWithContext(childCtx, request.method, request.apiURL, nil)
	} else {
		req, _ = http.NewRequestWithContext(childCtx, request.method, request.apiURL, request.requestBody)
	}

	tokenHeaderVal := fmt.Sprintf("Bearer %s", c.AccessToken)
	req.Header.Add("Authorization", tokenHeaderVal)
	req.Header.Add("Accept", "application/json")
	resp, errReq := c.HttpClient.Do(req)

	if errReq != nil {
		logger.Errorf("Error when getting response from api, the error message is %s", errReq)
		return []byte{}, errReq
	}
	defer resp.Body.Close()

	respBodyContent, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		logger.Errorf("Error when getting  content from response, the error message is %s", errReq)
		return []byte{}, errIOreader
	}
	return respBodyContent, nil
}

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

func (c *CPIClient) GetPackages() ([]CPIPackage, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.CpiApiURL)
	logger.Infof("Starting to get all packages from cpi tenant %s\n", fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []CPIPackage{}, errReq
	}

	var packcageResp PackagesResponse
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []CPIPackage{}, jsonUnmarshalError
	}

	return packcageResp.D.Results, nil
}

type PackageResponse struct {
	D CPIPackage `json:"d"`
}

func (c *CPIClient) GetPackage(packageID string) (CPIPackage, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')", c.CpiApiURL, packageID)
	logger.Infof("Starting to get packages %s from cpi tenant %s\n", packageID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return CPIPackage{}, errReq
	}
	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &packcageResp)

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

func (c *CPIClient) ImportPackage(cpiPackage importPackageRequest) (CPIPackage, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.CpiApiURL)
	logger.Infof("Starting to import package to cpi tenant %s\n", fullURL)
	requestBodyJson, _ := json.Marshal(cpiPackage)
	request := clientRequest{
		ctx:         childCtx,
		method:      http.MethodPost,
		apiURL:      fullURL,
		requestBody: bytes.NewBuffer(requestBodyJson),
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return CPIPackage{}, errReq
	}

	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &packcageResp)

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

func (c *CPIClient) GetPackageIflows(packageID string) ([]IflowItem, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts", c.CpiApiURL, packageID)
	logger.Infof("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []IflowItem{}, errReq
	}
	var iflowsResp PackageIflowsResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &iflowsResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return []IflowItem{}, jsonUnmarshalError
	}

	return iflowsResp.D.Results, nil
}

type IflowResp struct {
	D IflowItem `json:"d"`
}

func (c *CPIClient) GetPackageIflow(packageID string, iflowID string, iflowVersion string) (IflowItem, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiApiURL, packageID, iflowID, iflowVersion)
	logger.Infof("Starting to get iflow %s in package %s from cpi tenant %s\n", iflowID, packageID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return IflowItem{}, errReq
	}
	var iflowResp IflowResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &iflowResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return IflowItem{}, jsonUnmarshalError
	}

	return iflowResp.D, nil
}

func (c *CPIClient) GetIflow(iflowID string, iflowVersion string) (IflowItem, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()

	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiApiURL, iflowID, iflowVersion)

	logger.Infof("Starting to get iflow %s from cpi tenant %s\n", iflowID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return IflowItem{}, errReq
	}
	var iflowResp IflowResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &iflowResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return IflowItem{}, jsonUnmarshalError
	}

	return iflowResp.D, nil
}

func (c *CPIClient) DeployIflow(iflowID string, iflowVersion string) (string, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployIntegrationDesigntimeArtifact?Id='%s'&Version='%s'", c.CpiApiURL, iflowID, iflowVersion)
	logger.Infof("Starting to deploy iflow %s  on tenant %s\n", iflowID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodPost,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return taskID, errReq
	}
	taskID = string(respBodyContent)
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

func (c *CPIClient) CheckDeployStatus(taskID string) (string, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/BuildAndDeployStatus(TaskId='%s')", c.CpiApiURL, taskID)
	logger.Infof("Checking the deploy status for task id  %s on tenant %s\n", taskID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return "", errReq
	}
	var deployStatus DeployStatus
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &deployStatus)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return "", jsonUnmarshalError
	}

	return deployStatus.D.Status, nil

}

func (c *CPIClient) DeleteIflow(iflowID string, iflowVersion string) error {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiApiURL, iflowID, iflowVersion)
	logger.Infof("Starting to delete iflow %s on tenant %s\n", iflowID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodDelete,
		apiURL: fullURL,
	}
	_, errReq := c.Do(request)
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

func (c *CPIClient) GetPackageScripts(packageID string) ([]ScriptCollectionItem, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/ScriptCollectionDesigntimeArtifacts", c.CpiApiURL, packageID)
	logger.Infof("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return []ScriptCollectionItem{}, errReq
	}
	var scriptCollectionsResp ScriptCollectionsResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &scriptCollectionsResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return []ScriptCollectionItem{}, jsonUnmarshalError
	}

	return scriptCollectionsResp.D.Results, nil
}

type ScriptCollectionResp struct {
	D ScriptCollectionItem `json:"d"`
}

func (c *CPIClient) GetPackageScript(scriptCollectionID string, scriptCollectionVersion string) (ScriptCollectionItem, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiApiURL, scriptCollectionID, scriptCollectionVersion)
	logger.Infof("Starting to get script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodGet,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return ScriptCollectionItem{}, errReq
	}
	var scriptCollectionResp ScriptCollectionResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &scriptCollectionResp)

	if jsonUnmarshalError != nil {
		logger.Errorf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return ScriptCollectionItem{}, jsonUnmarshalError
	}

	return scriptCollectionResp.D, nil
}

func (c *CPIClient) DeployScriptCollection(scriptCollectionID string, scriptCollectionVersion string) (string, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployScriptCollectionDesigntimeArtifact(Id='%s',Version='%s')", c.CpiApiURL, scriptCollectionID, scriptCollectionVersion)
	logger.Infof("Starting to deploy script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodPost,
		apiURL: fullURL,
	}
	respBodyContent, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return "", errReq
	}
	taskID = string(respBodyContent)
	return taskID, nil
}

func (c *CPIClient) DeleteScriptCollection(scriptCollectionID string, scriptCollectionVersion string) error {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiApiURL, scriptCollectionID, scriptCollectionVersion)
	logger.Infof("Starting to delete script collection %s on tenant %s\n", scriptCollectionID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodDelete,
		apiURL: fullURL,
	}
	_, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return errReq
	}
	return nil

}

func (c *CPIClient) UndeployRuntimeArtifacts(artifactID string) error {

	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	fullURL := fmt.Sprintf("%s/IntegrationRuntimeArtifacts('%s')", c.CpiApiURL, artifactID)
	logger.Infof("Starting to undeploy artifact %s on tenant %s\n", artifactID, fullURL)
	request := clientRequest{
		ctx:    childCtx,
		method: http.MethodDelete,
		apiURL: fullURL,
	}
	_, errReq := c.Do(request)
	if errReq != nil {
		logger.Errorf("Error when getting response  content, the error message is %s", errReq)
		return errReq
	}
	return nil
}
