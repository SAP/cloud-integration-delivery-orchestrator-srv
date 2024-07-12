package cpi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	res, err := httpClient.Do(req)
	if err != nil {
		fmt.Errorf("Error when get the response, %s", err)
		return &CPIClient{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Errorf("Error when reading body from response, %s", err)
		return &CPIClient{}, err

	}

	var oauthResp OauthResp
	jsonUnmarshalErr := json.Unmarshal(body, &oauthResp)
	if jsonUnmarshalErr != nil {
		fmt.Errorf("Error when extract jsib data from response, %s", jsonUnmarshalErr)
		return &CPIClient{}, err
	}
	return &CPIClient{
		context:     ctx,
		HttpClient:  httpClient,
		AccessToken: oauthResp.AccessToken,
		CpiAPI:      cpiURL,
	}, nil
}

type PackageResponseItem struct {
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
		Results []PackageResponseItem `json:"results"`
	} `json:"d"`
}

func (c *CPIClient) GetPackages() (PackagesResponse, error) {
	fullURL := fmt.Sprintf("%s/IntegrationPackages", c.CpiAPI)
	fmt.Printf("Starting to get all packages from cpi tenant %s\n", fullURL)

	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	req, _ := http.NewRequestWithContext(childCtx, http.MethodGet, fullURL, nil)
	tokenHeaderVal := fmt.Sprintf("Bearer %s", c.AccessToken)
	req.Header.Add("Authorization", tokenHeaderVal)
	req.Header.Add("Accept", "application/json")
	resp, errReq := c.HttpClient.Do(req)
	fmt.Printf("resp status code %d\n", resp.StatusCode)
	if errReq != nil {
		fmt.Errorf("Error when getting response from api, the error message is %s", errReq)
		return PackagesResponse{}, errReq
	}

	defer resp.Body.Close()

	respBodyContent, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		fmt.Errorf("Error when getting content from response, error message %s", errIOreader)
		return PackagesResponse{}, errIOreader
	}

	var packcageResp PackagesResponse
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		fmt.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return PackagesResponse{}, jsonUnmarshalError
	}

	return packcageResp, nil
}

type PackageResponse struct {
	D PackageResponseItem `json:"d"`
}

func (c *CPIClient) GetPackage(packageID string) (PackageResponse, error) {
	fullURL := fmt.Sprintf("%s/IntegrationPackages('%s')", c.CpiAPI, packageID)
	fmt.Printf("Starting to get all packages from cpi tenant %s\n", fullURL)

	childCtx, cancel := context.WithCancel(c.context)
	defer cancel()
	req, _ := http.NewRequestWithContext(childCtx, http.MethodGet, fullURL, nil)
	tokenHeaderVal := fmt.Sprintf("Bearer %s", c.AccessToken)
	req.Header.Add("Authorization", tokenHeaderVal)
	req.Header.Add("Accept", "application/json")
	resp, errReq := c.HttpClient.Do(req)
	fmt.Printf("resp status code %d\n", resp.StatusCode)
	if errReq != nil {
		fmt.Errorf("Error when getting response from api, the error message is %s", errReq)
		return PackageResponse{}, errReq
	}

	defer resp.Body.Close()

	respBodyContent, errIOreader := io.ReadAll(resp.Body)

	if errIOreader != nil {
		fmt.Errorf("Error when getting content from response, error message %s", errIOreader)
		return PackageResponse{}, errIOreader
	}

	var packcageResp PackageResponse
	jsonUnmarshalError := json.Unmarshal(respBodyContent, &packcageResp)

	if jsonUnmarshalError != nil {
		fmt.Errorf("Error when unmarshal from json, error message %s", jsonUnmarshalError)
		return PackageResponse{}, jsonUnmarshalError
	}

	return packcageResp, nil
}

func (c *CPIClient) GetIflows() {

}

func (c *CPIClient) CheckIflows() {

}
func (c *CPIClient) GetScripts() {

}
