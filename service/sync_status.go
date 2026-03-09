package service

import (
	"context"
	"fmt"
	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/notify"
	"mmt-delivery/pkg/tms"
	"regexp"
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
	// Return error for canceled delivery requests
	if dr.AggregateStatus == lifecycle.AggCanceled {
		return fmt.Errorf("delivery request %d is already canceled", deliveryRequestID)
	}

	// sync import/deploy state after approval
	if conditions := syncImportState(deliveryRequestID, user); len(conditions) != 0 {
		BatchInsertConditions(conditions)
	}
	if conditions := syncDeployState(deliveryRequestID, user); len(conditions) != 0 {
		BatchInsertConditions(conditions)
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
	if err := db.Conn().Preload("DeliveryRule").First(&dr, deliveryRequestID).Error; err != nil {
		return []db.Condition{
			{
				DeliveryRequestID: deliveryRequestID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("failed to find delivery request %d: %s", deliveryRequestID, err.Error()),
			},
		}
	}

	// Build a set of target node IDs from delivery rule.
	// Only create/update operations for nodes included in the delivery rule.
	ruleTargetNodeIDs := make(map[uint]bool)
	for _, node := range dr.DeliveryRule.TargetNodes {
		ruleTargetNodeIDs[node.ID] = true
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

	conditions := make([]db.Condition, 0)
	for _, op := range artifactOps { // check to create new record if new tr status happens in tms
		trNumber := op.TransportRequestNumber
		if _, ok := trUpdated[trNumber]; ok { // prevent duplicate update
			continue
		}
		trUpdated[trNumber] = true
		// same artifactOp equals same trNumber, but in diffrent tms nodes
		for nID, nState := range trNodeStatus[trNumber] {
			// Skip nodes not in delivery rule - only process target nodes defined in the rule
			if _, ok := ruleTargetNodeIDs[nID]; !ok {
				env.Logger().Infof("skipping node %d for transport request %s: not in delivery rule target nodes", nID, trNumber)
				continue
			}

			// Query tenant by node ID from database
			tenant, err := queryTenantByNodeID(nID)
			if err != nil {
				conditions = append(conditions, db.Condition{
					DeliveryRequestID:         deliveryRequestID,
					ArtifactTenantOperationID: op.ID,
					State:                     lifecycle.CondWarn,
					Message:                   fmt.Sprintf("no cpi tenant configured for tms node %d: %s", nID, err.Error()),
				})
				continue
			}
			tenantID := tenant.ID

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
				env.Logger().Infof("no import state change for artifact %s(#%d) in node %d, current state: %s", curOp.ArtifactTechID, curOp.ID, nID, state)
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
				conditionMsg := fmt.Sprintf("artifact %s (version %s) has been successfully imported in tenant %d (node %d), at %s", curOp.ArtifactTechID, curOp.ArtifactVersion, tenantID, nID, nState.UpdatedAt)
				conditions = append(conditions, db.Condition{
					DeliveryRequestID:         deliveryRequestID,
					ArtifactTenantOperationID: curOp.ID,
					State:                     lifecycle.CondSuccess,
					Message:                   conditionMsg,
				})

				// Send notification to JIRA if configured
				if dr.JiraLink != "" {
					go func(jiraLink string, drID uint, message string) {
						issueKey := extractJiraIssueKey(jiraLink)
						if issueKey == "" {
							env.Logger().Warnf("Failed to extract JIRA issue key from link: %s", jiraLink)
							return
						}
						if err := notify.AddDeliveryComment(issueKey, drID, message, "Imported"); err != nil {
							env.Logger().Errorf("Failed to add JIRA comment for import success: %s", err)
						}
					}(dr.JiraLink, deliveryRequestID, conditionMsg)
				}
			}
			// get error logs if import failed
			if state == lifecycle.ImportFailed {
				logs, err := tmsClient.ErrLogsInTransportLog(trNumber, nID) // TODO: seems wrong function call.
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

// extractJiraIssueKey extracts JIRA issue key from JIRA URL
// Example: https://jira.tools.sap/browse/MACOMMT-32980 -> MACOMMT-32980
func extractJiraIssueKey(jiraURL string) string {
	// Pattern to match JIRA URLs like:
	// https://jira.tools.sap/browse/MACOMMT-32980
	// https://domain.atlassian.net/browse/PROJ-123
	// Extract issue key in format: PROJECT-ID (uppercase letters, hyphen, digits)
	re := regexp.MustCompile(`/browse/([A-Z]+-\d+)`)
	matches := re.FindStringSubmatch(jiraURL)
	if len(matches) > 1 {
		return matches[1]
	}

	env.Logger().Warn("Failed to extract JIRA issue key from URL: %s", jiraURL)
	return ""
}
