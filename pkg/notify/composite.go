package notify

import (
	"context"
	"fmt"
	"strings"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
)

// CompositeNotifier routes delivery events to the appropriate channel:
//   - OnDeliveryComment → Jira REST API (direct, via BTP Destination) + ANS event
//   - OnApprovalRequested / OnStatusChanged → SAP Alert Notification Service (if bound)
type CompositeNotifier struct {
	// JiraDest returns the Jira BTP Destination name, or "" if Jira is not configured/enabled.
	// Injected by main.go to break the dependency on db/gorm.
	jiraDest func() string
	// Resolver resolves BTP Destination credentials for Jira REST API calls.
	resolver *cf.DestinationServiceClient
	// Ans is the ANS Producer API client. Nil when ANS is not bound — silently skips.
	ans *AnsClient
}

// NewCompositeNotifier creates a notifier that combines Jira direct API + ANS event publishing.
// jiraDest is a function that returns the Jira destination name (injected to break DB dependency).
// ansClient may be nil (ANS not bound), in which case event methods are no-ops.
func NewCompositeNotifier(jiraDest func() string, resolver *cf.DestinationServiceClient, ansClient *AnsClient) *CompositeNotifier {
	return &CompositeNotifier{jiraDest: jiraDest, resolver: resolver, ans: ansClient}
}

func (n *CompositeNotifier) OnApprovalRequested(drID uint, requestor string, description string) error {
	if n.ans == nil {
		return nil
	}
	return n.ans.PublishEvent(context.Background(), AnsEvent{
		EventType: "delivery.approval.requested",
		Severity:  "INFO",
		Category:  "NOTIFICATION",
		Subject:   fmt.Sprintf("Approval Required: Delivery Request #%d", drID),
		Body:      fmt.Sprintf("Requestor: %s\nDescription: %s", requestor, description),
		Resource: AnsResource{
			ResourceName:     fmt.Sprintf("delivery-request-%d", drID),
			ResourceType:     "delivery-request",
			ResourceInstance: fmt.Sprintf("%d", drID),
		},
		Tags: map[string]string{"drId": fmt.Sprintf("%d", drID), "requestor": requestor},
	})
}

func (n *CompositeNotifier) OnStatusChanged(drID uint, status string, message string) error {
	if n.ans == nil {
		return nil
	}
	return n.ans.PublishEvent(context.Background(), AnsEvent{
		EventType: "delivery.status." + strings.ToLower(status),
		Severity:  "INFO",
		Category:  "NOTIFICATION",
		Subject:   fmt.Sprintf("Delivery Request #%d — %s", drID, status),
		Body:      message,
		Resource: AnsResource{
			ResourceName:     fmt.Sprintf("delivery-request-%d", drID),
			ResourceType:     "delivery-request",
			ResourceInstance: fmt.Sprintf("%d", drID),
		},
		Tags: map[string]string{"drId": fmt.Sprintf("%d", drID), "status": status},
	})
}

func (n *CompositeNotifier) OnDeliveryComment(issueKey string, drID uint, message string, status string) error {
	// Jira: direct REST API call (point-to-point)
	dest := n.jiraDest()
	if dest != "" {
		if err := AddDeliveryComment(n.resolver, dest, issueKey, drID, message, status); err != nil {
			return err
		}
	}
	// ANS: publish event so other channels (Email/Slack/Teams) are also notified
	if n.ans != nil {
		_ = n.ans.PublishEvent(context.Background(), AnsEvent{
			EventType: "delivery.comment.posted",
			Severity:  "INFO",
			Category:  "NOTIFICATION",
			Subject:   fmt.Sprintf("Delivery Request #%d — Jira comment (%s)", drID, status),
			Body:      message,
			Resource: AnsResource{
				ResourceName:     fmt.Sprintf("delivery-request-%d", drID),
				ResourceType:     "delivery-request",
				ResourceInstance: fmt.Sprintf("%d", drID),
			},
			Tags: map[string]string{"drId": fmt.Sprintf("%d", drID), "status": status, "issueKey": issueKey},
		})
	}
	return nil
}
