package service

import (
	"errors"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"time"
)

// handle delivery request approval logic

// add approvers
// currentUserID, approvers: subject/user_id in JWT claim
func RequestApproval(drID uint, currentUserID string, approvers []string, comment string) error {
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, drID).Error; err != nil {
		return fmt.Errorf("delivery request #%d not found", drID)
	}

	if dr.ApprovedAt != nil {
		return errors.New("delivery request already approved")
	}
	if dr.AggregateStatus != lifecycle.AggPending {
		return fmt.Errorf("only pending delivery request can be submitted for approval, current status: %s", dr.AggregateStatus)
	}
	now := time.Now()
	dr.AggregateStatus = lifecycle.AggWaitingApprove
	dr.Approvers = approvers
	dr.UpdatedBy, dr.UpdatedAt = currentUserID, now
	if err := db.Conn().Updates(&dr).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}
	// TODO: send email to approver, update JIRA

	return nil
}

// approverID: from JWT claim, user_id/subject
func Approve(drID uint, approverID string) error {
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, drID).Error; err != nil {
		return fmt.Errorf("delivery request #%d not found", drID)
	}
	if dr.AggregateStatus != lifecycle.AggWaitingApprove && dr.AggregateStatus != lifecycle.AggPending {
		return fmt.Errorf("cannot apprve delivery request %d: current status is %s", drID, dr.AggregateStatus)
	}
	if dr.CreatedBy == approverID {
		return fmt.Errorf("cannot approve your own delivery request")
	}
	dr.AggregateStatus = lifecycle.AggAwaitingImport
	now := time.Now()
	dr.ApprovedAt, dr.ApprovedBy = &now, approverID

	// TODO: send email, update JIRA

	if err := db.Conn().Updates(&dr).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}
	return nil
}
