package service

import (
	"context"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
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
func (s *Service) CancelDeliveryRequest(drID uint, userID string, reason string) error {
	before := s.captureDrSnapshot(drID)

	// 1. Best-effort sync to get latest state from TMS/CPI.
	// Ignored errors: "not approved yet" (PENDING/WAITING_APPROVAL are cancellable without sync),
	// "already in progress" (tracker is running — proceed with DB state as-is).
	// cancellableStatuses guard at step 3 is the authoritative safety check.
	if err := s.SyncDeliveryStatus(context.Background(), drID, userID); err != nil {
		if !strings.Contains(err.Error(), "has not been approved yet") &&
			!strings.Contains(err.Error(), "already in progress") {
			return fmt.Errorf("failed to sync delivery status before cancel: %s", err.Error())
		}
	}

	// 2. Re-query DR to get updated aggregate status (after sync)
	var dr db.DeliveryRequest
	if err := s.DB.First(&dr, drID).Error; err != nil {
		return fmt.Errorf("delivery request #%d not found: %s", drID, err.Error())
	}

	// 3. Validate current aggregate status
	if !cancellableStatuses[dr.AggregateStatus] {
		return fmt.Errorf("cannot cancel delivery request #%d with status %s", drID, dr.AggregateStatus)
	}

	// 4. Update delivery request status to CANCELED
	if err := s.DB.Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggCanceled,
		UpdatedBy:       userID,
	}).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}

	after := s.captureDrSnapshot(drID)
	if before.Exists && after.Exists && (before.Status != after.Status || before.OpsKey != after.OpsKey) {
		s.NotifyDrUpdated(drID)
	}

	// 5. Create cancellation condition
	userEmail, _ := s.GetUserEmail(context.Background(), userID)
	conditionMsg := fmt.Sprintf("Delivery request canceled by %s. Reason: %s", userEmail, reason)
	_ = s.BatchInsertConditions([]db.Condition{
		{
			DeliveryRequestID: drID,
			State:             lifecycle.CondWarn,
			Message:           conditionMsg,
		},
	})

	// 6. Send JIRA notification if configured (async)
	s.PostJiraComment(dr.JiraLink, drID, conditionMsg, "Canceled")

	// 7. Send email notification to related users (async)
	go func() {
		message := fmt.Sprintf("Delivery request #%d has been canceled by %s. Reason: %s", drID, userEmail, reason)
		recipients := []string{dr.CreatedBy, dr.UpdatedBy}
		if dr.ApprovedBy != "" {
			recipients = append(recipients, dr.ApprovedBy)
		}
		if err := s.Notifier.SendDeliveryNotification(recipients, drID, "Canceled", message); err != nil {
			s.Logger.Errorw("failed to send cancellation notification email", "dr_id", drID, "error", err)
		}
	}()

	return nil
}
