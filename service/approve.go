package service

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"time"
)

// handle delivery request approval logic

// add approvers
// currentUserID, approvers: subject/user_id in JWT claim
func (s *Service) RequestApproval(drID uint, currentUserID string, approvers []string, comment string) error {
	dr, err := s.QueryDrWithAssociations(drID)
	if err != nil {
		return fmt.Errorf("delivery request #%d not found", drID)
	}

	if dr.ApprovedAt != nil {
		return errors.New("delivery request already approved")
	}
	if dr.AggregateStatus != lifecycle.AggPending && dr.AggregateStatus != lifecycle.AggWaitingApprove {
		return fmt.Errorf("only pending delivery request can be submitted for approval, current status: %s", dr.AggregateStatus)
	}

	// Validate all ops have valid TRs before submitting for approval
	if _, err := s.BatchTrExist(context.Background(), dr.ArtifactTenantOperations, &dr.SourceTenant); err != nil {
		return err
	}

	requesterEmail, err := s.GetUserEmail(context.Background(), currentUserID)
	if err != nil {
		return err
	}
	// notify approvers asynchronously (non-critical)
	go func() {
		if err := s.Notifier.OnApprovalRequested(drID, requesterEmail, comment); err != nil {
			s.Logger.Errorw("failed to send approval request notification", "dr_id", drID, "error", err)
			_ = s.BatchInsertConditions([]db.Condition{
				{
					DeliveryRequestID: drID,
					State:             lifecycle.CondWarn,
					Message:           fmt.Sprintf("Failed to send approval request notification: %s", err.Error()),
				},
			})
		}
	}()
	if err := s.DB.Model(dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggWaitingApprove,
		Approvers:       approvers,
		UpdatedBy:       currentUserID,
	}).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}

	if err := s.BatchInsertConditions([]db.Condition{
		{
			DeliveryRequestID: drID,
			State:             lifecycle.CondSuccess,
			Message:           fmt.Sprintf("Delivery request submitted for approval by %s", requesterEmail),
		},
	}); err != nil {
		return fmt.Errorf("failed to update condition: %s", err.Error())
	}

	if dr.AggregateStatus != lifecycle.AggWaitingApprove {
		s.NotifyDrUpdated(drID)
	}
	return nil
}

// approverID: from JWT claim, user_id/subject
func (s *Service) Approve(drID uint, approverID string) (*db.DeliveryRequest, error) {
	dr, err := s.QueryDrWithAssociations(drID)
	if err != nil {
		return nil, err
	}
	if len(dr.ArtifactTenantOperations) == 0 {
		return nil, fmt.Errorf("cannot approve delivery request %d: no artifact operations found", drID)
	}
	if dr.AggregateStatus != lifecycle.AggWaitingApprove && dr.AggregateStatus != lifecycle.AggPending {
		return nil, fmt.Errorf("cannot apprve delivery request %d: current status is %s", drID, dr.AggregateStatus)
	}

	if dr.CreatedBy == approverID && !dr.DeliveryRule.SkipApprove {
		return nil, fmt.Errorf("cannot approve your own delivery request")
	}

	// Validate all ops have valid TRs before approving
	if _, err := s.BatchTrExist(context.Background(), dr.ArtifactTenantOperations, &dr.SourceTenant); err != nil {
		return nil, err
	}

	now := time.Now()
	// TR validation is done via BatchTrExist above, before setting approved status.
	if err := s.DB.Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggAwaitingImport,
		ApprovedBy:      approverID,
		ApprovedAt:      &now,
		UpdatedBy:       approverID,
	}).Error; err != nil {
		return nil, fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}

	// DB status committed — notify all subscribers on every exit path from here,
	// even if subsequent steps (email lookup, condition insert) fail.
	defer s.NotifyDrUpdated(drID)
	approverEmail, err := s.GetUserEmail(context.Background(), approverID)
	if err != nil {
		return nil, err
	}
	if err := s.BatchInsertConditions([]db.Condition{
		{
			DeliveryRequestID: drID,
			State:             lifecycle.CondSuccess,
			Message:           fmt.Sprintf("Delivery request approved by %s", approverEmail),
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to update condition: %s", err.Error())
	}

	// send notification asynchronously (non-critical)
	go func() {
		message := fmt.Sprintf("Delivery request #%d has been approved by %s", drID, approverID)
		if err := s.Notifier.OnStatusChanged(drID, "Approved", message); err != nil {
			s.Logger.Errorw("failed to send approval notification", "dr_id", drID, "error", err)
			_ = s.BatchInsertConditions([]db.Condition{
				{
					DeliveryRequestID: drID,
					State:             lifecycle.CondWarn,
					Message:           fmt.Sprintf("Failed to send approval notification: %s", err.Error()),
				},
			})
		}
	}()

	// One-shot sync: creates target ops so frontend can enable Import button.
	// Async — does not block Approve API response.
	// SyncDeliveryStatus internally calls NotifyDrUpdated when state changes.
	go func() {
		if err := s.SyncDeliveryStatus(context.Background(), drID, approverID); err != nil {
			s.Logger.Warnw("post-approve sync failed (non-fatal)", "dr_id", drID, "error", err)
		}
	}()

	return dr, nil
}
