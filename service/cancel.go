package service

import (
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/notify"
	"mmt-delivery/pkg/xsuaa"
)

// Statuses that allow cancellation
var cancellableStatuses = map[lifecycle.AggregateStatus]bool{
	lifecycle.AggPending:        true,
	lifecycle.AggWaitingApprove: true,
	lifecycle.AggAwaitingImport: true,
	lifecycle.AggImportFailed:   true,
	lifecycle.AggWaitingDeploy:  true,
	lifecycle.AggDeployFailed:   true,
}

// CancelDeliveryRequest cancels a delivery request with a reason.
// Cancellation is permanent and prevents any further import/deploy operations.
func CancelDeliveryRequest(drID uint, userID string, reason string) error {
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, drID).Error; err != nil {
		return fmt.Errorf("delivery request #%d not found: %s", drID, err.Error())
	}

	// Check if already canceled
	if dr.AggregateStatus == lifecycle.AggCanceled {
		return fmt.Errorf("delivery request #%d is already canceled", drID)
	}

	// Validate current aggregate status
	if !cancellableStatuses[dr.AggregateStatus] {
		return fmt.Errorf("cannot cancel delivery request #%d with status %s", drID, dr.AggregateStatus)
	}

	// Update delivery request status to CANCELED
	if err := db.Conn().Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggCanceled,
		UpdatedBy:       userID,
	}).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}

	// Create cancellation condition
	userEmail, _ := xsuaa.GetUserEmail(userID)
	conditionMsg := fmt.Sprintf("Delivery request canceled by %s. Reason: %s", userEmail, reason)
	_ = BatchInsertConditions([]db.Condition{
		{
			DeliveryRequestID: drID,
			State:             lifecycle.CondWarn,
			Message:           conditionMsg,
		},
	})

	// Send JIRA notification if configured (async)
	if dr.JiraLink != "" {
		go func(jiraLink string, drID uint, message string) {
			issueKey := extractJiraIssueKey(jiraLink)
			if issueKey == "" {
				return
			}
			if err := notify.AddDeliveryComment(issueKey, drID, message, "Canceled"); err != nil {
				env.Logger().Errorf("Failed to add JIRA comment for cancellation: %s", err)
			}
		}(dr.JiraLink, drID, conditionMsg)
	}

	// Send email notification to related users (async)
	go func() {
		message := fmt.Sprintf("Delivery request #%d has been canceled by %s. Reason: %s", drID, userEmail, reason)
		recipients := []string{dr.CreatedBy, dr.UpdatedBy}
		if dr.ApprovedBy != "" {
			recipients = append(recipients, dr.ApprovedBy)
		}
		if err := notify.SendDeliveryNotification(recipients, drID, "Canceled", message); err != nil {
			env.Logger().Errorf("Failed to send cancellation notification email: %s", err)
		}
	}()

	return nil
}
