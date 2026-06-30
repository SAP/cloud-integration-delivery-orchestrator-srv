package service

import (
	"context"
	"encoding/json"
	"fmt"
	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// drSnapshot captures the state of a DR at a point in time for change detection.
type drSnapshot struct {
	Exists bool
	Status lifecycle.AggregateStatus
	OpsKey string // serialized ops state for equality check
}

func (s *Service) captureDrSnapshot(drID uint) drSnapshot {
	var dr db.DeliveryRequest
	if err := s.DB.First(&dr, drID).Error; err != nil {
		return drSnapshot{Exists: false}
	}

	var ops []db.ArtifactTenantOperation
	s.DB.Where("delivery_request_id = ?", drID).Order("id ASC").Find(&ops)

	// Build a lightweight key for equality check (not sent over the wire)
	type opKey struct {
		ID     uint
		Import lifecycle.ImportState
		Deploy lifecycle.DeployState
	}
	keys := make([]opKey, 0, len(ops))
	for _, op := range ops {
		keys = append(keys, opKey{op.ID, op.ImportState, op.DeployState})
	}
	keyBytes, _ := json.Marshal(keys)

	return drSnapshot{
		Exists: true,
		Status: dr.AggregateStatus,
		OpsKey: string(keyBytes),
	}
}

func (s *Service) DetermineOverallStatus(drID uint) error {
	dr, err := s.QueryDrWithAssociations(drID)
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
		if err := s.DB.Model(&dr).Update("aggregate_status", newAggStatus).Error; err != nil {
			return fmt.Errorf("failed to update delivery request %d aggregate status: %s", drID, err.Error())
		}
	}
	return nil
}

func (s *Service) SyncDeliveryStatus(deliveryRequestID uint, user string) error {
	// Guard against concurrent sync for the same DR.
	// Two simultaneous calls both do queryOpsInDrWithAcc, both see no op for a
	// tenant, and both INSERT — producing duplicate ArtifactTenantOperation rows
	// that permanently diverge in import state.
	if _, loaded := s.drSyncLocks.LoadOrStore(deliveryRequestID, struct{}{}); loaded {
		return fmt.Errorf("sync for delivery request %d is already in progress", deliveryRequestID)
	}
	defer s.drSyncLocks.Delete(deliveryRequestID)

	before := s.captureDrSnapshot(deliveryRequestID)

	// Defers execute LIFO: notification runs last (registered first),
	// DetermineOverallStatus runs first (registered last), ensuring the
	// snapshot captured in the notify defer reflects the final aggregate status.
	defer func() {
		after := s.captureDrSnapshot(deliveryRequestID)
		if !before.Exists || !after.Exists {
			return
		}
		if before.OpsKey != after.OpsKey || before.Status != after.Status {
			s.NotifyDrUpdated(deliveryRequestID)
		}
	}()
	defer s.DetermineOverallStatus(deliveryRequestID)

	// Check if delivery request is approved before syncing status
	var dr db.DeliveryRequest
	if err := s.DB.First(&dr, deliveryRequestID).Error; err != nil {
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
	if conditions := s.syncImportState(deliveryRequestID, user); len(conditions) != 0 {
		s.BatchInsertConditions(conditions)
	}
	if conditions := s.syncDeployState(deliveryRequestID, user); len(conditions) != 0 {
		s.BatchInsertConditions(conditions)
	}
	return nil
}

// do this after sync import state
func (s *Service) syncDeployState(deliveryRequestID uint, user string) []db.Condition {
	var ops []db.ArtifactTenantOperation
	var err error
	conditions := make([]db.Condition, 0)

	if ops, err = s.queryOpsInDrWithAcc(deliveryRequestID); err != nil {
		return []db.Condition{
			{
				DeliveryRequestID: deliveryRequestID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("error occurred during sync deploy state: failed to query artifact operations in delivery request %d: %s", deliveryRequestID, err.Error()),
			},
		}
	}

	// Pre-load JiraLink once for JIRA notifications (avoids repeated DB queries per op)
	var dr db.DeliveryRequest
	s.DB.Select("jira_link").First(&dr, deliveryRequestID)

	for i := range ops {
		op := &ops[i]
		if op.DeployState != lifecycle.DeployInProgress {
			continue
		}
		cpiCli, err := s.CPI(context.Background(), op.Tenant.PirApiDestinationName)
		if err != nil {
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondError,
				Message:                   fmt.Sprintf("error occurred during sync deploy state: failed to create cpi client for tenant: %s: %s", op.Tenant.Name, err.Error()),
			})
			continue
		}
		rt, err := cpiCli.RuntimeArtifact(context.Background(), op.ArtifactTechID)
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
			// Version mismatch: runtime version differs from expected.
			// Compare versions to distinguish "deploy pending" from "superseded":
			// - runtime > expected → a higher version was deployed, this op is superseded
			// - runtime < expected → deploy hasn't taken effect yet, wait for next cycle
			rtV, opV := rt.Version, op.ArtifactVersion
			if !strings.HasPrefix(rtV, "v") {
				rtV = "v" + rtV
			}
			if !strings.HasPrefix(opV, "v") {
				opV = "v" + opV
			}
			if !semver.IsValid(rtV) || !semver.IsValid(opV) {
				s.Logger.Warn("invalid semver in deploy sync: runtime=%s, expected=%s for artifact %s in tenant %s", rt.Version, op.ArtifactVersion, op.ArtifactTechID, op.Tenant.Name)
				conditions = append(conditions, db.Condition{
					DeliveryRequestID:         deliveryRequestID,
					ArtifactTenantOperationID: op.ID,
					State:                     lifecycle.CondWarn,
					Message:                   fmt.Sprintf("cannot compare versions for artifact %s in tenant %s: runtime version %q or expected version %q is not valid semver", op.ArtifactTechID, op.Tenant.Name, rt.Version, op.ArtifactVersion),
				})
				continue
			}
			if semver.Compare(rtV, opV) > 0 {
				// Superseded: runtime has a higher version
				state = lifecycle.DeployFailed
				if err := s.DB.Model(&op).Updates(db.ArtifactTenantOperation{
					DeployState: state,
				}).Error; err != nil {
					conditions = append(conditions, db.Condition{
						DeliveryRequestID:         deliveryRequestID,
						ArtifactTenantOperationID: op.ID,
						State:                     lifecycle.CondError,
						Message:                   fmt.Sprintf("error occurred during sync deploy state for artifact %s in tenant %s: %s", op.ArtifactTechID, op.Tenant.Name, err.Error()),
					})
				} else {
					conditions = append(conditions, db.Condition{
						DeliveryRequestID:         deliveryRequestID,
						ArtifactTenantOperationID: op.ID,
						State:                     lifecycle.CondWarn,
						Message:                   fmt.Sprintf("runtime artifact %s version %s in tenant %s is higher than expected version %s. Deploy was superseded by another operation", op.ArtifactTechID, rt.Version, op.Tenant.Name, op.ArtifactVersion),
					})
				}
			}
			// runtime < expected: deploy still in progress, skip this cycle
			continue
		}
		if state == op.DeployState { // only need update if deploy state changed
			continue
		}
		if err := s.DB.Model(&op).Updates(db.ArtifactTenantOperation{
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
				Message:                   fmt.Sprintf("artifact %s (version %s) deploy failed in tenant %s. please check in CPI tenant %s", op.ArtifactTechID, op.ArtifactVersion, op.Tenant.Name, op.Tenant.PirApiDestinationName),
			})
		}
		// if deployed, save to condition and notify JIRA
		if state == lifecycle.DeployComplete {
			conditionMsg := fmt.Sprintf("artifact %s (version %s), deployed in %s. deployed by: %s, at: %s", op.ArtifactTechID, op.ArtifactVersion, op.Tenant.Name, rt.DeployedBy, rt.DeployedOn)
			conditions = append(conditions, db.Condition{
				DeliveryRequestID:         deliveryRequestID,
				ArtifactTenantOperationID: op.ID,
				State:                     lifecycle.CondSuccess,
				Message:                   conditionMsg,
			})
			s.PostJiraComment(dr.JiraLink, deliveryRequestID, conditionMsg, "Deployed")
		}
	}
	return conditions
}

// when call this function, make sure all ops have valid tr number
func (s *Service) syncImportState(deliveryRequestID uint, user string) []db.Condition {
	var artifactOps []db.ArtifactTenantOperation
	// Adjust the DB accessor (db.DB / db.GetDB()) to match your project setup
	artifactOps, err := s.queryOpsInDrWithAcc(deliveryRequestID)
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
	if err := s.DB.Preload("DeliveryRule").First(&dr, deliveryRequestID).Error; err != nil {
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

	trNodeStatus := make(map[string]map[uint]tms.TrNodeStatus)          // tr number status in all nodes. trNumber - map[nodeID]status
	tenantToOps := make(map[uint]map[string]db.ArtifactTenantOperation) // arTenantOp record in each node. cpi tenant ID - map[trNumber]ArtifactTenantOperation

	tmsClient, err := s.TmsSvc(context.Background())
	if err != nil {
		return []db.Condition{{
			DeliveryRequestID: deliveryRequestID,
			State:             lifecycle.CondError,
			Message:           fmt.Sprintf("error resolving TMS client: %s", err),
		}}
	}

	for _, op := range artifactOps {
		trNumber := op.TransportRequestNumber
		if _, ok := trNodeStatus[trNumber]; ok { // already fetched
			continue
		}
		// UpdateArtifactNodeStatus will call GetTransportRequest internally
		ns, err := tmsClient.TrNodeStatuses(context.Background(), trNumber)
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
				s.Logger.Infof("skipping node %d for transport request %s: not in delivery rule target nodes", nID, trNumber)
				continue
			}

			// Query tenant by node ID from database
			tenant, err := s.queryTenantByNodeID(nID)
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
				deployState := lifecycle.DeployNotStarted
				if op.SkipDeploy {
					deployState = lifecycle.DeployDisabled
				}
				newOp := db.ArtifactTenantOperation{
					DeliveryRequestID:      op.DeliveryRequestID,
					ArtifactTechID:         op.ArtifactTechID,
					ArtifactVersion:        op.ArtifactVersion,
					ArtifactName:           op.ArtifactName,
					ArtifactType:           op.ArtifactType,
					PackageID:              op.PackageID,
					PackageName:            op.PackageName,
					PackageVersion:         op.PackageVersion,
					TenantID:               tenantID,
					TransportRequestNumber: trNumber,
					SkipDeploy:             op.SkipDeploy,
					ImportState:            lifecycle.ImportNotStarted,
					DeployState:            deployState,
					CreatedBy:              user,
				}
				tenantToOps[tenantID][trNumber] = newOp
			}
			curOp := tenantToOps[tenantID][trNumber]

			// NOTE: determine import state
			state := lifecycle.DeriveImport(nState.Status)
			if state == curOp.ImportState { // skip if state no change
				s.Logger.Infof("no import state change for artifact %s(#%d) in node %d, current state: %s", curOp.ArtifactTechID, curOp.ID, nID, state)
				continue
			}

			// update state only if changed
			curOp.ImportState, curOp.UpdatedBy = state, user
			// NOTE: set deploy state if import completed
			if curOp.ImportState == lifecycle.ImportComplete && curOp.DeployState == lifecycle.DeployNotStarted {
				if curOp.SkipDeploy {
					curOp.DeployState = lifecycle.DeployDisabled
				} else {
					curOp.DeployState = lifecycle.DeployQueued
				}
			}
			if err := s.DB.Save(&curOp).Error; err != nil { // update each op
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

				// Send notification to JIRA if configured (same as SUCCEEDED; WARNING is still a successful import)
				s.PostJiraComment(dr.JiraLink, deliveryRequestID, conditionMsg, "Imported")

				// TMS WARNING: import counts as complete but persist severity-W log lines as CondWarn
				if strings.EqualFold(nState.Status, "WARNING") {
					warnMsgs, werr := tmsClient.WarnLogsInTransportLog(context.Background(), trNumber, nID)
					var warnBody string
					if werr != nil {
						warnBody = fmt.Sprintf("could not load TMS warning messages for transport request %s in node %d: %s", trNumber, nID, werr.Error())
					} else if len(warnMsgs) == 0 {
						warnBody = fmt.Sprintf("TMS reported WARNING for transport request %s in node %d; no severity W messages in transport log.", trNumber, nID)
					} else {
						warnBody = strings.Join(warnMsgs, "\n")
					}
					conditions = append(conditions, db.Condition{
						DeliveryRequestID:         deliveryRequestID,
						ArtifactTenantOperationID: curOp.ID,
						State:                     lifecycle.CondWarn,
						Message:                   warnBody,
					})
				}
			}
			// get error logs if import failed
			if state == lifecycle.ImportFailed {
				logs, err := tmsClient.ErrLogsInTransportLog(context.Background(), trNumber, nID)
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
func (s *Service) extractJiraIssueKey(jiraURL string) string {
	// Pattern to match JIRA URLs like:
	// https://jira.tools.sap/browse/MACOMMT-32980
	// https://domain.atlassian.net/browse/PROJ-123
	// Extract issue key in format: PROJECT-ID (uppercase letters, hyphen, digits)
	re := regexp.MustCompile(`/browse/([A-Z]+-\d+)`)
	matches := re.FindStringSubmatch(jiraURL)
	if len(matches) > 1 {
		return matches[1]
	}

	s.Logger.Warn("Failed to extract JIRA issue key from URL: %s", jiraURL)
	return ""
}

// PostJiraComment sends a JIRA comment for a delivery request asynchronously.
// Preconditions checked (all must pass, otherwise silently skips):
//   - DR has a non-empty JiraLink
//   - JiraLink can be parsed to extract issue key
//   - JIRA integration is enabled and destination is configured
//
// Safe to call from any goroutine — fires and forgets.
func (s *Service) PostJiraComment(jiraLink string, drID uint, message string, status string) {
	if jiraLink == "" {
		return
	}
	issueKey := s.extractJiraIssueKey(jiraLink)
	if issueKey == "" {
		return
	}
	go func() {
		if err := s.Notifier.AddDeliveryComment(issueKey, drID, message, status); err != nil {
			s.Logger.Errorf("Failed to post JIRA comment (DR #%d, status=%s): %s", drID, status, err)
		}
	}()
}
