package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"
	"net/http"
)

// JiraClient represents a JIRA API client
type JiraClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

// JiraIssue represents a JIRA issue/task
type JiraIssue struct {
	ID    string                 `json:"id"`
	Key   string                 `json:"key"`
	Self  string                 `json:"self"`
	Fields map[string]interface{} `json:"fields"`
}

// JiraComment represents a JIRA comment
type JiraComment struct {
	ID           string      `json:"id"`
	Self         string      `json:"self"`
	Body         string      `json:"body"`
	Created      string      `json:"created"`
	Updated      string      `json:"updated"`
	Author       JiraUser    `json:"author"`
	UpdateAuthor JiraUser    `json:"updateAuthor"`
	Visibility   *JiraVisibility `json:"visibility,omitempty"`
}

// JiraUser represents a JIRA user
type JiraUser struct {
	AccountID   string `json:"accountId"`
	Active      bool   `json:"active"`
	DisplayName string `json:"displayName"`
	Self        string `json:"self"`
}

// JiraVisibility represents comment visibility restrictions
type JiraVisibility struct {
	Identifier string `json:"identifier"`
	Type       string `json:"type"`
	Value      string `json:"value"`
}

// JiraSubtask represents a JIRA subtask
type JiraSubtask struct {
	ID           string `json:"id"`
	OutwardIssue struct {
		ID      string `json:"id"`
		Key     string `json:"key"`
		Self    string `json:"self"`
		Fields  struct {
			Status struct {
				Name     string `json:"name"`
				IconURL  string `json:"iconUrl"`
			} `json:"status"`
			Summary   string `json:"summary"`
		} `json:"fields"`
	} `json:"outwardIssue"`
	Type struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
}

// AddCommentRequest represents the request body for adding a comment
type AddCommentRequest struct {
	Body       string             `json:"body"`
	Visibility *JiraVisibility    `json:"visibility,omitempty"`
}

// NewJiraClient creates a new JIRA client by resolving the given destination name.
func NewJiraClient(resolver *cf.DestinationServiceClient, destName string) (*JiraClient, error) {
	dest, err := resolver.GetDestination(context.Background(), destName)
	if err != nil {
		return nil, fmt.Errorf("failed to get JIRA destination '%s': %s", destName, err)
	}
	if dest == nil {
		return nil, fmt.Errorf("JIRA destination '%s' not found", destName)
	}

	return &JiraClient{
		baseURL:    dest.URL,
		username:   dest.User,
		password:   dest.Password,
		httpClient: &http.Client{},
	}, nil
}

// GetIssue retrieves a JIRA issue by key
func (c *JiraClient) GetIssue(issueKey string) (*JiraIssue, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s", c.baseURL, issueKey)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %s", err)
	}

	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get issue, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var issue JiraIssue
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, fmt.Errorf("failed to decode response: %s", err)
	}

	return &issue, nil
}

// AddComment adds a comment to a JIRA issue
func (c *JiraClient) AddComment(issueKey string, comment string) (*JiraComment, error) {
	return c.AddCommentWithOptions(issueKey, comment, nil)
}

// AddCommentWithOptions adds a comment to a JIRA issue with visibility options
func (c *JiraClient) AddCommentWithOptions(issueKey string, comment string, visibility *JiraVisibility) (*JiraComment, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s/comment", c.baseURL, issueKey)

	requestBody := AddCommentRequest{
		Body:       comment,
		Visibility: visibility,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %s", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %s", err)
	}

	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to add comment, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var jiraComment JiraComment
	if err := json.NewDecoder(resp.Body).Decode(&jiraComment); err != nil {
		return nil, fmt.Errorf("failed to decode response: %s", err)
	}

	return &jiraComment, nil
}

// GetComments retrieves all comments for a JIRA issue
func (c *JiraClient) GetComments(issueKey string) ([]JiraComment, error) {
	url := fmt.Sprintf("%s/rest/api/2/issue/%s/comment", c.baseURL, issueKey)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %s", err)
	}

	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get comments, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Comments []JiraComment `json:"comments"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %s", err)
	}

	return response.Comments, nil
}

// GetIssueSummary retrieves the summary of a JIRA issue
func (c *JiraClient) GetIssueSummary(issueKey string) (string, error) {
	issue, err := c.GetIssue(issueKey)
	if err != nil {
		return "", err
	}

	if summary, ok := issue.Fields["summary"].(string); ok {
		return summary, nil
	}

	return "", fmt.Errorf("summary not found in issue fields")
}

// GetIssueStatus retrieves the status of a JIRA issue
func (c *JiraClient) GetIssueStatus(issueKey string) (string, error) {
	issue, err := c.GetIssue(issueKey)
	if err != nil {
		return "", err
	}

	if status, ok := issue.Fields["status"].(map[string]interface{}); ok {
		if name, ok := status["name"].(string); ok {
			return name, nil
		}
	}

	return "", fmt.Errorf("status not found in issue fields")
}

// GetIssueDescription retrieves the description of a JIRA issue
func (c *JiraClient) GetIssueDescription(issueKey string) (string, error) {
	issue, err := c.GetIssue(issueKey)
	if err != nil {
		return "", err
	}

	if description, ok := issue.Fields["description"].(string); ok {
		return description, nil
	}

	return "", fmt.Errorf("description not found in issue fields")
}

// GetSubtasks retrieves all subtasks for a JIRA issue
func (c *JiraClient) GetSubtasks(issueKey string) ([]JiraSubtask, error) {
	issue, err := c.GetIssue(issueKey)
	if err != nil {
		return nil, err
	}

	// Field name is "sub-tasks" with a hyphen
	subtasksField, ok := issue.Fields["sub-tasks"]
	if !ok {
		// No subtasks field, return empty slice
		return []JiraSubtask{}, nil
	}

	// Convert subtasks field to JSON and unmarshal into []JiraSubtask
	subtasksJSON, err := json.Marshal(subtasksField)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal subtasks: %s", err)
	}

	var subtasks []JiraSubtask
	if err := json.Unmarshal(subtasksJSON, &subtasks); err != nil {
		return nil, fmt.Errorf("failed to unmarshal subtasks: %s", err)
	}

	return subtasks, nil
}

// AddDeliveryComment adds a delivery-related comment to a JIRA issue
func AddDeliveryComment(resolver *cf.DestinationServiceClient, destName string, issueKey string, deliveryRequestID uint, message string, status string) error {
	client, err := NewJiraClient(resolver, destName)
	if err != nil {
		env.Logger().Errorf("Failed to create JIRA client: %s", err)
		return err
	}

	comment := fmt.Sprintf("**Delivery Request #%d**\n\n*Status: %s*\n\n%s", deliveryRequestID, status, message)

	_, err = client.AddComment(issueKey, comment)
	if err != nil {
		env.Logger().Errorf("Failed to add comment to JIRA issue %s: %s", issueKey, err)
		return err
	}

	env.Logger().Infow("added comment to JIRA issue", "issue_key", issueKey, "dr_id", deliveryRequestID)
	return nil
}
