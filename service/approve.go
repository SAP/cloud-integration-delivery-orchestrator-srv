package service

import (
	"errors"
	"fmt"
	"mmt-delivery/db"
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
	if dr.AggregateStatus != AggPending && dr.AggregateStatus != AggWaitingApprove {
		return fmt.Errorf("only pending delivery request can be submitted for approval, current status: %s", dr.AggregateStatus)
	}
	// TODO: send email to approver, update JIRA
	sendMailto := sendMailto(dr.Approvers, approvers)
	if err := notify.SendEmail(sendMailto); err != nil {
		return fmt.Errorf("failed to nofity approvers via email: %s", err.Error())
	}
	if err := db.Conn().Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: AggWaitingApprove,
		Approvers:       approvers,
		UpdatedBy:       currentUserID,
	}).Error; err != nil {
		return fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}
	return nil
}

// approverID: from JWT claim, user_id/subject
func Approve(drID uint, approverID string) (*db.DeliveryRequest, error) {
	dr, err := QueryDrWithAcc(drID)
	if err != nil {
		return nil, err
	}
	if len(dr.ArtifactTenantOperations) == 0 {
		return nil, fmt.Errorf("cannot approve delivery request %d: no artifact operations found", drID)
	}
	if dr.AggregateStatus != AggWaitingApprove && dr.AggregateStatus != AggPending {
		return nil, fmt.Errorf("cannot apprve delivery request %d: current status is %s", drID, dr.AggregateStatus)
	}

	// TODO: prevent self-approval
	// if dr.CreatedBy == approverID {
	// 	return fmt.Errorf("cannot approve your own delivery request")
	// }
	now := time.Now()
	// TODO: send email, update JIRA
	if err := notify.SendEmail([]string{approverID, dr.CreatedBy, dr.UpdatedBy}); err != nil {
		return nil, fmt.Errorf("failed to nofity approvers via email: %s", err.Error())
	}
	// no need to call TrExist, for it will be done in update/insert ops.
	if err := db.Conn().Model(&dr).Updates(db.DeliveryRequest{
		AggregateStatus: AggAwaitingImport,
		ApprovedBy:      approverID,
		ApprovedAt:      &now,
		UpdatedBy:       approverID,
	}).Error; err != nil {
		return nil, fmt.Errorf("failed to update delivery request status: %s", err.Error())
	}
	// sync import/deploy state after approval
	if err := SyncImportState(drID, approverID); err != nil {
		return nil, fmt.Errorf("failed to sync import state: %s", err.Error())
	}
	if err := SyncDeployState(drID, approverID); err != nil {
		return nil, fmt.Errorf("failed to sync deploy state: %s", err.Error())
	}

	return dr, nil
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
