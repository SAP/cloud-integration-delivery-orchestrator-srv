package service

import (
	"errors"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/notify"
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
	if dr.AggregateStatus != lifecycle.AggPending && dr.AggregateStatus != lifecycle.AggWaitingApprove {
		return fmt.Errorf("only pending delivery request can be submitted for approval, current status: %s", dr.AggregateStatus)
	}
	// TODO: send email to approver, update JIRA
	sendMailto := sendMailto(dr.Approvers, approvers)
	if err := notify.SendEmail(sendMailto); err != nil {
		return fmt.Errorf("failed to nofity approvers via email: %s", err.Error())
	}
	if err := db.Conn().Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggWaitingApprove,
		Approvers:       approvers, UpdatedBy: currentUserID,
	}).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}
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
	now := time.Now()
	// TODO: send email, update JIRA
	if err := notify.SendEmail([]string{approverID, dr.CreatedBy, dr.UpdatedBy}); err != nil {
		return fmt.Errorf("failed to nofity approvers via email: %s", err.Error())
	}

	if err := db.Conn().Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: lifecycle.AggAwaitingImport, ApprovedBy: approverID, ApprovedAt: &now,
		UpdatedBy: approverID,
	}).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}
	return nil
}

// existAppr: already send mail to; newAppr: receive from http request.
func sendMailto(existAppr []string, newAppr []string) []string {
	markSent := make(map[string]bool) // mark user id that already send to approvers
	for _, uid := range existAppr {
		markSent[uid] = true
	}
	sendMailto := make([]string, 0)
	for _, appr := range newAppr {
		if _, ok := markSent[appr]; !ok {
			sendMailto = append(sendMailto, appr)
		}
	}
	return sendMailto
}
