package service

import (
	"context"
	"fmt"
	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/tms"
	"strings"
)

// INITIAL, RUNNING, SUCCEEDED, FATAL
func DeriveImport(state string) ImportState {
	switch strings.ToUpper(state) {
	case "INITIAL":
		return ImportQueued
	case "RUNNING":
		return ImportInProgress
	case "SUCCEEDED":
		return ImportComplete
	case "FATAL", "FAILED", "ERROR":
		return ImportFailed
	case "PARTIAL":
		return ImportPartial
	default:
		return ImportNotStarted
	}
}

// do this after sync import state
func SyncDeployState(deliveryRequestID uint, user string) error {
	var ops []db.ArtifactTenantOperation
	var err error
	if ops, err = queryOpsInDrWithAcc(deliveryRequestID); err != nil {
		return err
	}
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		if op.DeployState != DeployInProgress {
			continue
		}
		cpiCli, err := cpi.NewClient(context.Background(), op.Tenant.CpiEndpoint.Name)
		if err != nil {
			errOps[op.ID] = fmt.Errorf("failed to create cpi client for tenant %s: %s", op.Tenant.Name, err)
			// TODO: add condition
			continue
		}
		rt, err := cpiCli.RuntimeArtifact(op.ArtifactTechID)
		if err != nil {
			// if api return error, may not deployed yet, just continue
			errOps[op.ID] = fmt.Errorf("failed to get runtime artifact %s in tenant %s: %s", op.ArtifactTechID, op.Tenant.Name, err)
			// TODO: add a condition
		}
		var state DeployState
		if _, ok := errOps[op.ID]; ok {
			state = DeployFailed
		} else if rt.Version == op.ArtifactVersion {
			switch rt.Status {
			case consts.Artifact_Rt_Started:
				state = DeployComplete
			case consts.Artifact_Rt_Starting:
				state = DeployInProgress
			case consts.Artifact_Rt_Error:
				state = DeployFailed
			}
		} else {
			continue // not triggered by this operation
		}
		if state == op.DeployState { // only need update if deploy state changed
			continue
		}
		if err := db.Conn().Model(&op).Updates(db.ArtifactTenantOperation{
			DeployState: state, UpdatedBy: user,
		}).Error; err != nil {
			return fmt.Errorf("error when creating new artifact tenant operation for artifact %s in tenant %d: %w", op.ArtifactTechID, op.TenantID, err)
		}
	}
	return nil
}

// when call this function, make sure all ops have valid tr number
func SyncImportState(deliveryRequestID uint, user string) error {
	var artifactOps []db.ArtifactTenantOperation
	// Adjust the DB accessor (db.DB / db.GetDB()) to match your project setup
	artifactOps, err := queryOpsInDrWithAcc(deliveryRequestID)
	if err != nil {
		return err
	}
	// find delivery rule id in delivery request
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, deliveryRequestID).Error; err != nil {
		return fmt.Errorf("failed to find delivery request %d: %w", deliveryRequestID, err)
	}
	drRuleID := dr.DeliveryRuleID
	if drRuleID == 0 {
		return fmt.Errorf("delivery request %d has no delivery rule", deliveryRequestID)
	}
	var deliveryRule db.DeliveryRule
	if err := db.Conn().First(&deliveryRule, drRuleID).Error; err != nil {
		return fmt.Errorf("failed to find delivery rule %d: %w", drRuleID, err)
	}

	tmsClient, err := tms.NewClient(context.Background())
	if err != nil {
		return fmt.Errorf("error creating tms client: %w", err)
	}
	trNodeStatus := make(map[string]map[uint]tms.TrNodeStatus)          // tr number status in all nodes. trNumber - map[nodeID]status
	tenantToOps := make(map[uint]map[string]db.ArtifactTenantOperation) // arTenantOp record in each node. cpi tenant ID - map[trNumber]ArtifactTenantOperation
	//
	for _, op := range artifactOps {
		trNumber := op.TransportRequestNumber
		if _, ok := trNodeStatus[trNumber]; ok { // already fetched
			continue
		}
		// UpdateArtifactNodeStatus will call GetTransportRequest internally
		ns, err := tmsClient.TrNodeStatuses(trNumber)
		if err != nil {
			return fmt.Errorf("error when getting transport request %s: %w", trNumber, err)
		}
		trNodeStatus[trNumber] = ns
	}

	for _, op := range artifactOps {
		tenantID, trNumber := op.Tenant.ID, op.TransportRequestNumber
		if _, ok := tenantToOps[tenantID]; !ok {
			tenantToOps[tenantID] = make(map[string]db.ArtifactTenantOperation)
		}
		if _, ok := tenantToOps[tenantID][trNumber]; ok { // already mapped
			continue
		}
		tenantToOps[tenantID][trNumber] = op
	}
	trUpdated := make(map[string]bool)
	nodetoTenantID := nodeTenantCache // tms node ID - cpi tenant ID
	for _, op := range artifactOps {  // check to create new record if new tr status happens in tms
		trNumber := op.TransportRequestNumber
		if _, ok := trUpdated[trNumber]; ok { // prevent duplicate update
			continue
		}
		trUpdated[trNumber] = true
		// same artifactOp equals same trNumber, but in diffrent tms nods
		for nID, nState := range trNodeStatus[trNumber] {
			var tenantID uint
			var ok bool
			// TODO: skip node that is not in delivery rule. currently will create op for all nodes, see dr id #20

			if tenantID, ok = nodetoTenantID[nID]; !ok {
				return fmt.Errorf("no cpi tenant found for tms node %d, please configure Cpi Tenant first", nID)
			}
			if _, ok := tenantToOps[tenantID]; !ok {
				tenantToOps[tenantID] = make(map[string]db.ArtifactTenantOperation)
			}
			if _, ok := tenantToOps[tenantID][trNumber]; !ok { // means this a new status happens in tms, should create a new record
				newOp := db.ArtifactTenantOperation{
					DeliveryRequestID:      op.DeliveryRequestID,
					ArtifactID:             op.ArtifactID,
					ArtifactTechID:         op.ArtifactTechID,
					ArtifactVersion:        op.ArtifactVersion,
					TenantID:               tenantID,
					TransportRequestNumber: trNumber,
					ImportState:            ImportNotStarted,
					DeployState:            DeployNotStarted,
					CreatedBy:              user,
				}
				tenantToOps[tenantID][trNumber] = newOp
			}
			curOp := tenantToOps[tenantID][trNumber]
			state := DeriveImport(nState.Status)
			if state == curOp.ImportState {
				continue
			}
			// update state if changed
			curOp.ImportState, curOp.UpdatedBy = state, user
			// NOTE: set deploy state if import completed
			if curOp.ImportState == ImportComplete && curOp.DeployState == DeployNotStarted {
				curOp.DeployState = DeployQueued
			}
			if err := db.Conn().Save(&curOp).Error; err != nil {
				return fmt.Errorf("error when creating new artifact tenant operation for artifact %s in node %d: %w", curOp.ArtifactTechID, nID, err)
			}

		}

	}
	return nil
}
