package service

import (
	"context"
	"fmt"
	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
	"strings"
)

func DetermineOverallStatus(drID uint) error {
	dr, err := QueryDrWithAssociations(drID)
	if err != nil {
		return fmt.Errorf("failed to query delivery request %d: %s", drID, err.Error())
	}
	sourceTenantID := dr.SourceTenantID

	// derive aggregate status (exclude source tenant operations)
	newAggStatus := lifecycle.DeriveAggregateStatus(dr.AggregateStatus, func() []lifecycle.ImportState {
		states := make([]lifecycle.ImportState, 0)
		for _, op := range dr.ArtifactTenantOperations {
			// Skip source tenant - aggregate status should only reflect target tenants
			if op.TenantID == sourceTenantID {
				continue
			}
			states = append(states, op.ImportState)
		}
		return states
	}(), func() []lifecycle.DeployState {
		states := make([]lifecycle.DeployState, 0)
		for _, op := range dr.ArtifactTenantOperations {
			// Skip source tenant - aggregate status should only reflect target tenants
			if op.TenantID == sourceTenantID {
				continue
			}
			states = append(states, op.DeployState)
		}
		return states
	}())
	if newAggStatus != dr.AggregateStatus {
		if err := db.Conn().Model(&dr).Update("aggregate_status", newAggStatus).Error; err != nil {
			return fmt.Errorf("failed to update delivery request %d aggregate status: %s", drID, err.Error())
		}
	}
	return nil
}

func SyncDeliveryStatus(deliveryRequestID uint, user string) error {
	// Always recompute aggregate status regardless of early returns below

	defer DetermineOverallStatus(deliveryRequestID)

	// Check if delivery request is approved before syncing status
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, deliveryRequestID).Error; err != nil {
		return fmt.Errorf("failed to find delivery request %d: %s", deliveryRequestID, err.Error())
	}
	if dr.ApprovedAt == nil || dr.ApprovedBy == "" {
		return fmt.Errorf("delivery request %d has not been approved yet", deliveryRequestID)
	}

	// sync import/deploy state after approval
	if conditions := syncImportState(deliveryRequestID, user); len(conditions) != 0 {
		BatchInsertConditions(conditions)
		return fmt.Errorf("failed to sync import state. see conditions for details")
	}
	if conditions := syncDeployState(deliveryRequestID, user); len(conditions) != 0 {
		BatchInsertConditions(conditions)
		return fmt.Errorf("failed to sync deploy state. see logs for detail")
	}
	return nil
}

// do this after sync import state
func syncDeployState(deliveryRequestID uint, user string) []db.Condition {
	var ops []db.ArtifactTenantOperation
	var err error
	conditions := make([]db.Condition, 0)

	if ops, err = queryOpsInDrWithAcc(deliveryRequestID); err != nil {
		return []db.Condition{
			{
				DeliveryRequestID: deliveryRequestID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("error occurred during sync deploy state: failed to query artifact operations in delivery request %d: %s", deliveryRequestID, err.Error()),
			},
		}
	}
	for i := range ops {
		op := &ops[i]
		if op.DeployState != lifecycle.DeployInProgress {
			continue
		}
		cpiCli, err := cpi.NewClient(context.Background(), op.Tenant.CpiEndpoint.Name)
		if err != nil {
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondError,
				Message:                   fmt.Sprintf("error occurred during sync deploy state: failed to create cpi client for tenant: %s: %s", op.Tenant.Name, err.Error()),
			})
			continue
		}
		rt, err := cpiCli.RuntimeArtifact(op.ArtifactTechID)
		if err != nil {
			// if api return error, may not deployed yet, just continue
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondWarn,
				Message:                   fmt.Sprintf("failed to get runtime artifact %s in tenant %s(may not deployed yet): %s", op.ArtifactTechID, op.Tenant.Name, err.Error()),
			})
			continue
		}
		// NOTE: determine deploy state
		var state lifecycle.DeployState
		if rt.Version == op.ArtifactVersion {
			switch rt.Status {
			case consts.Artifact_Rt_Started:
				state = lifecycle.DeployComplete
			case consts.Artifact_Rt_Starting:
				state = lifecycle.DeployInProgress
			case consts.Artifact_Rt_Error:
				state = lifecycle.DeployFailed
			}
		} else {
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondWarn,
				Message:                   fmt.Sprintf("runtime artifact %s version %s in tenant %s does not match expected version %s. May not triggered by this operation", op.ArtifactTechID, rt.Version, op.Tenant.Name, op.ArtifactVersion),
			})
			continue // not triggered by this operation
		}
		if state == op.DeployState { // only need update if deploy state changed
			continue
		}
		if err := db.Conn().Model(&op).Updates(db.ArtifactTenantOperation{
			DeployState: state, // state sync no need to update other fields, like UpdatedBy
		}).Error; err != nil {
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondError,
				Message:                   fmt.Sprintf("error occurred during sync deploy state for artifact %s in tenant %s: %s", op.ArtifactTechID, op.Tenant.Name, err.Error()),
			})
			continue
		}
		// if error, also save to condition
		if state == lifecycle.DeployFailed {
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondError,
				Message:                   fmt.Sprintf("artifact %s (version %s) deploy failed in tenant %s. please check in CPI tenant %s", op.ArtifactTechID, op.ArtifactVersion, op.Tenant.Name, op.Tenant.CpiEndpoint.Name),
			})
		}
		// if deployed, save to condition
		if state == lifecycle.DeployComplete {
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondSuccess,
				Message:                   fmt.Sprintf("artifact %s (version %s), deployed in %s. deployed by: %s, at: %s", op.ArtifactTechID, op.ArtifactVersion, op.Tenant.Name, rt.DeployedBy, rt.DeployedOn),
			})
		}
	}
	return conditions
}

// when call this function, make sure all ops have valid tr number
func syncImportState(deliveryRequestID uint, user string) []db.Condition {
	var artifactOps []db.ArtifactTenantOperation
	// Adjust the DB accessor (db.DB / db.GetDB()) to match your project setup
	artifactOps, err := queryOpsInDrWithAcc(deliveryRequestID)
	if err != nil {
		return []db.Condition{
			{
				DeliveryRequestID: deliveryRequestID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("error occurred during sync import state: failed to query artifact operations in delivery request %d: %s", deliveryRequestID, err.Error()),
			},
		}
	}
	// find delivery rule id in delivery request
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, deliveryRequestID).Error; err != nil {
		return []db.Condition{
			{
				DeliveryRequestID: deliveryRequestID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("failed to find delivery request %d: %s", deliveryRequestID, err.Error()),
			},
		}
	}

	tmsClient, err := tms.NewClient(context.Background())
	if err != nil {
		return []db.Condition{
			{
				DeliveryRequestID: deliveryRequestID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("error creating tms client: %s", err.Error()),
			},
		}
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
			return []db.Condition{
				{
					DeliveryRequestID:         deliveryRequestID,
					State:                     lifecycle.CondError,
					ArtifactTenantOperationID: op.ID,
					Message:                   fmt.Sprintf("error when getting transport request %s: %s", trNumber, err.Error()),
				},
			}
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

	conditions := make([]db.Condition, 0)
	for _, op := range artifactOps { // check to create new record if new tr status happens in tms
		trNumber := op.TransportRequestNumber
		if _, ok := trUpdated[trNumber]; ok { // prevent duplicate update
			continue
		}
		trUpdated[trNumber] = true
		// same artifactOp equals same trNumber, but in diffrent tms nods
		for nID, nState := range trNodeStatus[trNumber] {
			var tenantID uint
			var ok bool
			// TODO: skip node that is not in delivery rule.
			// currently will create op for all nodes.
			if tenantID, ok = nodetoTenantID[nID]; !ok {
				return []db.Condition{
					{
						DeliveryRequestID:         deliveryRequestID,
						State:                     lifecycle.CondError,
						ArtifactTenantOperationID: op.ID,
						Message:                   fmt.Sprintf("no cpi tenant found for tms node %d, please configure Cpi Tenant first", nID),
					},
				}
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
					ImportState:            lifecycle.ImportNotStarted,
					DeployState:            lifecycle.DeployNotStarted,
					CreatedBy:              user,
				}
				tenantToOps[tenantID][trNumber] = newOp
			}
			curOp := tenantToOps[tenantID][trNumber]

			// NOTE: determine import state
			state := lifecycle.DeriveImport(nState.Status)
			if state == curOp.ImportState { // skip if state no change
				continue
			}

			// update state only if changed
			curOp.ImportState, curOp.UpdatedBy = state, user
			// NOTE: set deploy state if import completed
			if curOp.ImportState == lifecycle.ImportComplete && curOp.DeployState == lifecycle.DeployNotStarted {
				curOp.DeployState = lifecycle.DeployQueued
			}
			if err := db.Conn().Save(&curOp).Error; err != nil { // update each op
				return []db.Condition{
					{
						DeliveryRequestID:         deliveryRequestID,
						State:                     lifecycle.CondError,
						ArtifactTenantOperationID: curOp.ID,
						Message:                   fmt.Sprintf("error when creating new artifact tenant operation for artifact %s in node %d: %s", curOp.ArtifactTechID, nID, err.Error()),
					},
				}
			}
			// if imported, save to condition
			if state == lifecycle.ImportComplete {
				conditions = append(conditions, db.Condition{
					DeliveryRequestID:         deliveryRequestID,
					ArtifactTenantOperationID: curOp.ID,
					State:                     lifecycle.CondSuccess,
					Message:                   fmt.Sprintf("artifact %s (version %s) has been successfully imported in tenant %d (node %d), at %s", curOp.ArtifactTechID, curOp.ArtifactVersion, tenantID, nID, nState.UpdatedAt),
				})
			}
			// get error logs if import failed
			if state == lifecycle.ImportFailed {
				logs, err := tmsClient.ErrLogsInTransportLog(trNumber, nID)
				var message string
				if err != nil {
					message = fmt.Sprintf("error when getting error logs for transport request %s in node %d: %s", trNumber, nID, err.Error())
				} else {
					message = strings.Join(logs, "\n")
				}
				conditions = append(conditions, db.Condition{
					DeliveryRequestID:         deliveryRequestID,
					State:                     lifecycle.CondError,
					ArtifactTenantOperationID: curOp.ID,
					Message:                   message,
				})
			}
		}

	}
	return conditions
}
