package service

import (
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/notify"
	"mmt-delivery/pkg/xsuaa"
	"strings"
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
	// 1. Sync status first to get latest state from TMS/CPI
	if err := SyncDeliveryStatus(drID, userID); err != nil {
		// Ignore "not approved yet" error - PENDING/WAITING_APPROVAL are cancellable
		// For these statuses, no operations have started yet, so DB status is accurate
		if !strings.Contains(err.Error(), "has not been approved yet") {
			return fmt.Errorf("failed to sync delivery status before cancel: %s", err.Error())
		}
	}

	// 2. Re-query DR to get updated aggregate status (after sync)
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, drID).Error; err != nil {
		return fmt.Errorf("delivery request #%d not found: %s", drID, err.Error())
	}

	// 3. Validate current aggregate status
	if !cancellableStatuses[dr.AggregateStatus] {
		return fmt.Errorf("cannot cancel delivery request #%d with status %s", drID, dr.AggregateStatus)
	}

	// 4. Update delivery request status to CANCELED
	if err := db.Conn().Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggCanceled,
		UpdatedBy:       userID,
	}).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}

	// 5. Create cancellation condition
	userEmail, _ := xsuaa.GetUserEmail(userID)
	conditionMsg := fmt.Sprintf("Delivery request canceled by %s. Reason: %s", userEmail, reason)
	_ = BatchInsertConditions([]db.Condition{
		{
			DeliveryRequestID: drID,
			State:             lifecycle.CondWarn,
			Message:           conditionMsg,
		},
	})

	// 6. Send JIRA notification if configured (async)
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

	// 7. Send email notification to related users (async)
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
