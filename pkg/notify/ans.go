package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
)

// AnsEvent is the payload sent to the ANS Producer API.
type AnsEvent struct {
	EventType      string            `json:"eventType"`
	Subject        string            `json:"subject"`
	Body           string            `json:"body"`
	Severity       string            `json:"severity"` // FATAL|ERROR|WARNING|INFO
	Category       string            `json:"category"` // ALERT|NOTIFICATION|UPDATE|EXCEPTION
	Resource       AnsResource       `json:"resource"`
	Tags           map[string]string `json:"tags,omitempty"`
	EventTimestamp int64             `json:"eventTimestamp,omitempty"`
}

// AnsResource describes the BTP resource the event pertains to.
type AnsResource struct {
	ResourceName     string            `json:"resourceName"`
	ResourceType     string            `json:"resourceType"`
	ResourceInstance string            `json:"resourceInstance,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
}

// AnsClient publishes events to the SAP Alert Notification Service Producer API.
// Nil-safe: construct via NewAnsClient; returns nil when credentials are absent.
type AnsClient struct {
	client *env.HttpClient
}

// NewAnsClient creates an ANS client from environment credentials.
// Returns nil if ANS is not bound (optional service).
func NewAnsClient() *AnsClient {
	creds := env.AnsCredential()
	if creds == nil {
		return nil
	}
	client, err := env.NewClient(context.Background(), creds.Clientid, creds.Clientsecret, creds.AuthUrl, creds.ApiUrl)
	if err != nil {
		return nil
	}
	return &AnsClient{client: client}
}

// Endpoint returns the ANS API base URL (for display in System Config).
func (c *AnsClient) Endpoint() string {
	return c.client.ApiURL
}

// TestConnection verifies that the ANS OAuth2 credentials are valid
// by making a lightweight request to the ANS API.
func (c *AnsClient) TestConnection(ctx context.Context) error {
	_, err := c.client.Do(ctx, &env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: strings.TrimRight(c.client.ApiURL, "/") + "/cf/consumer/v1/resource-events?page=0&pageSize=1",
	})
	return err
}

// PublishEvent sends a single event to the ANS Producer API.
func (c *AnsClient) PublishEvent(ctx context.Context, event AnsEvent) error {
	if event.EventTimestamp == 0 {
		event.EventTimestamp = 0 // ANS will use server time if omitted
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("ans: marshal event: %w", err)
	}

	_, err = c.client.Do(ctx, &env.HttpRequest{
		Method:      http.MethodPost,
		ApiURL:      strings.TrimRight(c.client.ApiURL, "/") + "/cf/producer/v1/resource-events",
		RequestBody: payload,
	})
	return err
}
