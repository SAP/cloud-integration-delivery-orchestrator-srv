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
	if _, err := s.BatchTrExist(dr.ArtifactTenantOperations, &dr.SourceTenant); err != nil {
		return err
	}

	requesterEmail, err := s.GetUserEmail(context.Background(), currentUserID)
	if err != nil {
		return err
	}
	// send email to approver asynchronously
	sendMailto := s.sendMailto(dr.Approvers, approvers)
	if len(sendMailto) > 0 {
		go func() {
			if err := s.Notifier.SendApprovalRequest(sendMailto, drID, requesterEmail, comment); err != nil {
				// Log email error as condition
				s.Logger.Error("Failed to send approval request email: %s", err)
				_ = s.BatchInsertConditions([]db.Condition{
					{
						DeliveryRequestID: drID,
						State:             lifecycle.CondWarn,
						Message:           fmt.Sprintf("Failed to send approval request email to approvers: %s", err.Error()),
					},
				})
			}
		}()
	}
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
	if _, err := s.BatchTrExist(dr.ArtifactTenantOperations, &dr.SourceTenant); err != nil {
		return nil, err
	}

	now := time.Now()
	// send email notification asynchronously
	go func() {
		message := fmt.Sprintf("Delivery request #%d has been approved by %s", drID, approverID)
		if err := s.Notifier.SendDeliveryNotification(
			[]string{approverID, dr.CreatedBy, dr.UpdatedBy}, drID, "Approved", message,
		); err != nil {
			// Log email error as condition
			s.Logger.Error("Failed to send approval notification email: %s", err)
			_ = s.BatchInsertConditions([]db.Condition{
				{
					DeliveryRequestID: drID,
					State:             lifecycle.CondWarn,
					Message:           fmt.Sprintf("Failed to send approval notification email: %s", err.Error()),
				},
			})
		}
	}()
	// TR validation is done via BatchTrExist above, before setting approved status.
	if err := s.DB.Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggAwaitingImport,
		ApprovedBy:      approverID,
		ApprovedAt:      &now,
		UpdatedBy:       approverID,
	}).Error; err != nil {
		return nil, fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}
	// sync import/deploy state after approval
	if err := s.SyncDeliveryStatus(drID, approverID); err != nil {
		return nil, err
	}
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
	return dr, nil
}

// existAppr: already send mail to; newAppr: receive from http request.
func (s *Service) sendMailto(existAppr []string, newAppr []string) []string {
	markSent := make(map[string]bool) // mark user id that already send to approvers
	for _, uid := range existAppr {
		markSent[uid] = true
	}
	sendMailto := make([]string, 0)
	for _, appr := range newAppr {
		if _, ok := markSent[appr]; !ok {
			// Convert XSUAA user ID to email address
			email, err := s.GetUserEmail(context.Background(), appr)
			if err != nil {
				s.Logger.Error("Failed to get email for user %s: %s", appr, err)
				continue // skip if failed to get email
			}
			sendMailto = append(sendMailto, email)
		}
	}
	return sendMailto
}
