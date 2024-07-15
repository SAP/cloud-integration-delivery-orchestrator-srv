package cpi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type CPIClient struct {
	context     context.Context
	HttpClient  *http.Client
	AccessToken string
	CpiAPI      string
}

type OauthResp struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Jti         string `json:"jti"`
}

func NewCPIClient(ctx context.Context, clientID string, clientSecret string, cpiAuthURL string, cpiURL string) (*CPIClient, error) {
	payload := strings.NewReader(fmt.Sprintf("grant_type=client_credentials&client_id=%s&client_secret=%s", clientID, clientSecret))

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cpiAuthURL, payload)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")
	httpClient := http.DefaultClient

	res, errReq := httpClient.Do(req)
	if errReq != nil {
		log.Printf("Error when get the response, %s", errReq)
		return &CPIClient{}, errReq
	}
	defer res.Body.Close()
	body, errIOReader := io.ReadAll(res.Body)
	if errIOReader != nil {
		log.Printf("Error when reading body from response, %s", errIOReader)
		return &CPIClient{}, errIOReader
	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		log.Printf("Error when extract jsib data from response, %s", jsonUnmarshalErr)
		return &CPIClient{}, jsonUnmarshalErr
	}
	return &CPIClient{
		context:     ctx,
		HttpClient:  httpClient,
		AccessToken: oauthResp.AccessToken,
		CpiAPI:      cpiURL,
	}, nil
}

func (c *CPIClient) Do(apiURL string, method string) ([]byte, error) {
	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	req, _ := http.NewRequestWithContext(childCtx, method, apiURL, nil)
	tokenHeaderVal := fmt.Sprintf("Bearer %s", c.AccessToken)
	req.Header.Add("Authorization", tokenHeaderVal)
	req.Header.Add("Accept", "application/json")
	resp, errReq := c.HttpClient.Do(req)

	if errReq != nil {
		log.Printf("Error when getting response from api, the error message is %s", errReq)
		return []byte{}, errReq
	}
	defer resp.Body.Close()

	respBodyContent, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		log.Printf("Error when getting  content from response, the error message is %s", errReq)
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
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.CpiAPI)
	log.Printf("Starting to get all packages from cpi tenant %s\n", fullURL)
	respBodyContent, errReq := c.Do(fullURL, http.MethodGet)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return []CPIPackage{}, errReq
	}

	var packcageResp PackagesResponse
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return []CPIPackage{}, jsonUnmarshalError
	}

	return packcageResp.D.Results, nil
}

type PackageResponse struct {
	D CPIPackage `json:"d"`
}

func (c *CPIClient) GetPackage(packageID string) (CPIPackage, error) {
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')", c.CpiAPI, packageID)
	log.Printf("Starting to get packages %s from cpi tenant %s\n", packageID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodGet)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return CPIPackage{}, errReq
	}
	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
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

type IflowsResp struct {
	D struct {
		Results []IflowItem `json:"results"`
	} `json:"d"`
}

func (c *CPIClient) GetIflows(packageID string) ([]IflowItem, error) {

	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts", c.CpiAPI, packageID)
	log.Printf("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodGet)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return []IflowItem{}, errReq
	}
	var iflowsResp IflowsResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &iflowsResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return []IflowItem{}, jsonUnmarshalError
	}

	return iflowsResp.D.Results, nil
}

type IflowResp struct {
	D IflowItem `json:"d"`
}

func (c *CPIClient) GetIflow(packageID string, iflowID string, iflowVersion string) (IflowItem, error) {

	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiAPI, packageID, iflowID, iflowVersion)
	log.Printf("Starting to get iflow %s in package %s from cpi tenant %s\n", iflowID, packageID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodGet)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return IflowItem{}, errReq
	}
	var iflowResp IflowResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &iflowResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return IflowItem{}, jsonUnmarshalError
	}

	return iflowResp.D, nil
}

func (c *CPIClient) DeployIflow(packageID string, iflowID string, iflowVersion string) (string, error) {
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployIntegrationDesigntimeArtifact?Id='%s'&Version='%s'", c.CpiAPI, iflowID, iflowVersion)
	log.Printf("Starting to deploy iflow %s  in package %s on tenant %s\n", iflowID, packageID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodPost)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
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
	fullURL := fmt.Sprintf("%s/BuildAndDeployStatus(TaskId='%s')", c.CpiAPI, taskID)
	log.Printf("Checking the deploy status for task id  %s on tenant %s\n", taskID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodGet)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return "", errReq
	}
	var deployStatus DeployStatus
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &deployStatus)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return "", jsonUnmarshalError
	}

	return deployStatus.D.Status, nil

}

func (c *CPIClient) DeleteIflow(packageID string, iflowID string, iflowVersion string) error {
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiAPI, iflowID, iflowVersion)
	log.Printf("Starting to delete iflow %s in package %s on tenant %s\n", iflowID, packageID, fullURL)

	_, errReq := c.Do(fullURL, http.MethodDelete)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
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

func (c *CPIClient) GetScripts(packageID string) ([]ScriptCollectionItem, error) {
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')/IntegrationDesigntimeArtifacts", c.CpiAPI, packageID)
	log.Printf("Starting to get all iflows in package %s from cpi tenant %s\n", packageID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodGet)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return []ScriptCollectionItem{}, errReq
	}
	var scriptCollectionsResp ScriptCollectionsResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &scriptCollectionsResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return []ScriptCollectionItem{}, jsonUnmarshalError
	}

	return scriptCollectionsResp.D.Results, nil
}

type ScriptCollectionResp struct {
	D ScriptCollectionItem `json:"d"`
}

func (c *CPIClient) GetScript(scriptCollectionID string, scriptCollectionVersion string) (ScriptCollectionItem, error) {

	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiAPI, scriptCollectionID, scriptCollectionVersion)
	log.Printf("Starting to get script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodGet)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return ScriptCollectionItem{}, errReq
	}
	var scriptCollectionResp ScriptCollectionResp
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &scriptCollectionResp)

	if jsonUnmarshalError != nil {
		log.Printf("Error when unmarshal from json, error message %s\n", jsonUnmarshalError)
		return ScriptCollectionItem{}, jsonUnmarshalError
	}

	return scriptCollectionResp.D, nil
}

func (c *CPIClient) DeployScriptCollection(packageID string, scriptCollectionID string, scriptCollectionVersion string) (string, error) {
	var taskID string
	fullURL := fmt.Sprintf("%s/DeployScriptCollectionDesigntimeArtifact(Id='%s',Version='%s')", c.CpiAPI, scriptCollectionID, scriptCollectionVersion)
	log.Printf("Starting to deploy script collection %s in package from cpi tenant %s\n", scriptCollectionID, fullURL)

	respBodyContent, errReq := c.Do(fullURL, http.MethodPost)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return "", errReq
	}
	taskID = string(respBodyContent)
	return taskID, nil
}

func (c *CPIClient) DeleteScriptCollection(packageID string, scriptCollectionID string, scriptCollectionVersion string) error {

	fullURL := fmt.Sprintf("%s/ScriptCollectionDesigntimeArtifacts(Id='%s',Version='%s')", c.CpiAPI, scriptCollectionID, scriptCollectionVersion)
	log.Printf("Starting to delete script collection %s in package %s on tenant %s\n", scriptCollectionID, packageID, fullURL)

	_, errReq := c.Do(fullURL, http.MethodDelete)
	if errReq != nil {
		log.Printf("Error when getting response  content, the error message is %s", errReq)
		return errReq
	}
	return nil

}
