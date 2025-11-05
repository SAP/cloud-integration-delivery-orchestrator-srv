package service

// delivery request related services

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
	"time"

	"gorm.io/gorm"
)

var nodeTenantCache map[uint]uint

// map tms node ID to cpi tenant ID
func init() {
	nodeTenantCache = make(map[uint]uint)
	// load all tenants
	var tenants []db.CpiTenant
	if err := db.Conn().Find(&tenants).Error; err != nil {
		panic("failed to load cpi tenants: " + err.Error())
	}
	for _, t := range tenants {
		nodeTenantCache[t.TransportNodeID] = t.ID
	}
}
func BatchTrExist(ops []db.ArtifactTenantOperation, sourceTenant *db.CpiTenant) (bool, error) {
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		_, err := TrExist(op, sourceTenant)
		if err != nil {
			errOps[op.ID] = err
		}
	}
	if len(errOps) > 0 {
		errMsg := "transport request existence check failed for some artifact tenant operations:\n"
		for id, err := range errOps {
			errMsg += fmt.Sprintf("  operation %d: %s\n", id, err)
		}
		return false, errors.New(errMsg)
	}
	return true, nil
}

// check tr Number existence and origin
func TrExist(op *db.ArtifactTenantOperation, sourceTenant *db.CpiTenant) (bool, error) {
	trNumber := op.TransportRequestNumber
	if trNumber == "" {
		return false, fmt.Errorf("artifact %s has empty transport request number", op.ArtifactTechID)
	}
	tmsCli, err := tms.NewClient(context.Background())
	if err != nil {
		return false, fmt.Errorf("error when creating tms client: %s", err)
	}
	trV1, err := tmsCli.GetTransportRequest(trNumber) // v1 to check state
	if err != nil {
		return false, fmt.Errorf("error when getting transport request %s, the tr number may not exist, error message: %s", trNumber, err)
	}
	if trV1 == nil || trV1.ID == 0 || trV1.State != "RELEASED" { // only released tr can be imported
		return false, fmt.Errorf("artifact %s has invalid transport request number %s", op.ArtifactTechID, trNumber)
	}
	if trV1.Origin != sourceTenant.TransportNodeName { // check if match source tenant. can only be checked by origin node name, not id.
		return false, fmt.Errorf("artifact %s has transport request number %s not from source tenant node %s", op.ArtifactTechID, trNumber, sourceTenant.TransportNodeName)
	}
	// check Content Field, should match techID, Version, Type
	// index := -1
	// for i, md := range trV1.Content[0].Metadata {
	// 	if md.Name == op.ArtifactTechID || md.Type == op.Artifact.Type || md.Version == op.ArtifactVersion {
	// 		index = i
	// 		break
	// 	}
	// }
	// if index == -1 {
	// 	return false, fmt.Errorf("artifact %s, trNumber %s: not match. May use a wrong trNumber for this artifact", op.ArtifactTechID, trNumber)
	// }

	// update status of artifact tenant operation
	return true, nil
}

// check and load artifact info into db, set ArtifactID in ops
func LoadArtifact(op db.ArtifactTenantOperation) (atf db.Artifact, err error) {
	a := &op.Artifact
	if db.Conn().FirstOrCreate(a, &db.Artifact{TechID: a.TechID, Version: a.Version}).Error != nil {
		err = fmt.Errorf("error when saving artifact %s:%s", a.TechID, a.Version)
		return
	}
	atf = *a
	return
}

// with preloaded Tenant and Artifact
func queryOpsInDrWithAcc(drID uint) (ops []db.ArtifactTenantOperation, err error) {
	if err = db.Conn().Where(&db.ArtifactTenantOperation{DeliveryRequestID: drID}).
		Preload("Tenant").
		Preload("Artifact").
		Find(&ops).Error; err != nil {
		err = fmt.Errorf("db query failed: %w", err)
		return
	}
	if len(ops) == 0 {
		err = fmt.Errorf("no artifact tenant operation found for delivery request %d", drID)
		return
	}
	return
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
		if op.DeployState != lifecycle.DeployInProgress {
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
		var state lifecycle.DeployState
		if _, ok := errOps[op.ID]; ok {
			state = lifecycle.DeployFailed
		} else if rt.Version == op.ArtifactVersion {
			switch rt.Status {
			case consts.Artifact_Rt_Started:
				state = lifecycle.DeployComplete
			case consts.Artifact_Rt_Starting:
				state = lifecycle.DeployInProgress
			case consts.Artifact_Rt_Error:
				state = lifecycle.DeployFailed
			}
		} else {
			continue // not triggered by this operation
		}
		if state != op.DeployState {
			op.DeployState = state
			op.UpdatedAt, op.UpdatedBy = time.Now(), user
		}
		if err := db.Conn().Updates(&op).Error; err != nil {
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

	nodetoTenantID := nodeTenantCache // tms node ID - cpi tenant ID
	for _, op := range artifactOps {  // check to create new record if new tr status happens in tms
		trNumber := op.TransportRequestNumber
		// same artifactOp equals same trNumber, but in diffrent tms nods
		for nID, nState := range trNodeStatus[trNumber] {
			var tenantID uint
			var ok bool
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
					ImportState:            lifecycle.ImportNotStarted,
					DeployState:            lifecycle.DeployNotStarted,
					CreatedBy:              user,
				}
				tenantToOps[tenantID][trNumber] = newOp
			}
			curOp := tenantToOps[tenantID][trNumber]
			state := lifecycle.DeriveImport(nState.Status)
			if state == curOp.ImportState {
				continue
			}
			// update state if changed
			curOp.ImportState = state
			curOp.UpdatedAt, curOp.UpdatedBy = time.Now(), user
			// NOTE: set deploy state if import completed
			if curOp.ImportState == lifecycle.ImportComplete && curOp.DeployState == lifecycle.DeployNotStarted {
				curOp.DeployState = lifecycle.DeployQueued
			}
			if err := db.Conn().Save(&curOp).Error; err != nil {
				return fmt.Errorf("error when creating new artifact tenant operation for artifact %s in node %d: %w", curOp.ArtifactTechID, nID, err)
			}

		}

	}
	return nil
}

// generate source tenant, included routes and nodes from included tenants
func SourceAndRoute(includedTenants []db.CpiTenant) (sorceTenant *db.CpiTenant, includedRoutes []db.TransportRoute, includedNodes []db.TransportNode, err error) {
	// generate Transport routes
	var tmsCli *tms.TmsClient
	if tmsCli, err = tms.NewClient(context.Background()); err != nil {
		return
	}
	var transportRoutes []db.TransportRoute
	if transportRoutes, err = tmsCli.GetRoutes(); err != nil {
		return
	}
	var transportNodes []db.TransportNode
	if transportNodes, err = tmsCli.GetNodes(); err != nil {
		return
	}
	nodeAll := make(map[uint]db.TransportNode) // all nodes map
	for _, n := range transportNodes {
		nodeAll[n.ID] = n
	}

	includedNodes = make([]db.TransportNode, 0)
	includedRoutes = make([]db.TransportRoute, 0)

	nodeIDs := make(map[uint]bool) // nodeid - tenant
	for i := range includedTenants {
		t := &includedTenants[i]
		nodeIDs[t.TransportNodeID] = true
		includedNodes = append(includedNodes, nodeAll[t.TransportNodeID])
	}
	targetNodeIDs := make(map[uint]bool) // to determine source node
	for _, r := range transportRoutes {
		if nodeIDs[r.SourceNodeID] && nodeIDs[r.TargetNodeID] {
			includedRoutes = append(includedRoutes, r)
			targetNodeIDs[r.TargetNodeID] = true
		}
	}
	for i := range includedTenants {
		t := &includedTenants[i]
		if !targetNodeIDs[t.TransportNodeID] {
			sorceTenant = t
			break
		}
	}

	if sorceTenant == nil {
		err = fmt.Errorf("no source node found among included tenants' transport nodes")
		return
	}
	return
}

// downstreamfromSource returns all downstream routes and nodes reachable from a source node (BFS).
// It avoids cycles and duplicate routes/nodes.
// NOTE: the source node itself is NOT included in the returned targetNodes.
func downstreamfromSource(sourceNodeID uint, transportNodes []db.TransportNode, transportRoutes []db.TransportRoute) (targetRoutes []db.TransportRoute, targetNodes []db.TransportNode) {
	if sourceNodeID == 0 || len(transportRoutes) == 0 {
		return
	}

	// Build node lookup
	nodeMap := make(map[uint]db.TransportNode, len(transportNodes))
	for _, n := range transportNodes {
		nodeMap[n.ID] = n
	}

	// Adjacency: sourceNodeID -> routes originating there
	adj := make(map[uint][]db.TransportRoute)
	for _, r := range transportRoutes {
		adj[r.SourceNodeID] = append(adj[r.SourceNodeID], r)
	}

	if _, ok := nodeMap[sourceNodeID]; !ok {
		return
	}

	visitedNodes := make(map[uint]bool)
	visitedNodes[sourceNodeID] = true
	visitedRoutes := make(map[uint]bool)

	queue := []uint{sourceNodeID}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, r := range adj[curr] {
			// Skip duplicate route
			if visitedRoutes[r.ID] {
				continue
			}
			visitedRoutes[r.ID] = true
			targetRoutes = append(targetRoutes, r)

			// Enqueue target node if not seen
			if visitedNodes[r.TargetNodeID] {
				continue
			}
			if trNode, ok := nodeMap[r.TargetNodeID]; ok {
				targetNodes = append(targetNodes, trNode)
				visitedNodes[r.TargetNodeID] = true
				queue = append(queue, r.TargetNodeID)
			}
		}
	}

	return
}

func GetDeliveryRule(drRuleID uint) (db.DeliveryRule, error) {
	var rule db.DeliveryRule
	if err := db.Conn().First(&rule, drRuleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return rule, fmt.Errorf("delivery rule %d not found", drRuleID)
		}
		return rule, fmt.Errorf("failed to get delivery rule %d: %s", drRuleID, err)
	}
	return rule, nil
}

// generate route info for delivery rule
func GenRouteForRule(ruleID uint) (err error) {
	rule, err := GetDeliveryRule(ruleID)
	if err != nil {
		return
	}
	sourceTenant, targetRoutes, targetNodes, err := SourceAndRoute(rule.IncludedTenants)
	if err != nil {
		return
	}
	rule.TargetNodes, rule.TargetRoutes = targetNodes, targetRoutes
	rule.SourceTenantID = sourceTenant.ID

	if err := db.Conn().Save(&rule).Error; err != nil {
		return err
	}

	return
}

func DeleteTenantOps(opIDs []uint) error {
	if len(opIDs) == 0 {
		return nil
	}
	errOps := make(map[uint]error)
	for _, id := range opIDs {
		if id == 0 {
			return fmt.Errorf("invalid operation id 0")
		}
		var op db.ArtifactTenantOperation
		if err := db.Conn().First(&op, id).Error; err != nil {
			errOps[id] = fmt.Errorf("failed to find artifact tenant operation %d. Op may not exists: %s", id, err)
		}
		// check state before delete
		if op.RequestState != lifecycle.RequestPending {
			errOps[id] = fmt.Errorf("cannot delete artifact tenant operation %d in state %s. Can disable delivery", id, op.RequestState)
		}
		if err := db.Conn().Delete(&db.ArtifactTenantOperation{}, id).Error; err != nil {
			errOps[id] = fmt.Errorf("failed to delete artifact operation %d: %s", id, err)
		}
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during delete artifact tenant operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\t operation %d: %s\n", id, e)
		}
		return errors.New(errMsg)
	}
	return nil
}

func InsertTenantOps(drID uint, ops []db.ArtifactTenantOperation, user string) error {
	if len(ops) == 0 {
		return nil
	}
	for i := range ops {
		op := &ops[i]
		a, err := LoadArtifact(*op)
		if err != nil {
			return fmt.Errorf("failed to load artifact for operation %d: %s", op.ID, err)
		}
		op.CreatedAt, op.CreatedBy = time.Now(), user
		op.Artifact = a
		op.DeliveryRequestID = drID
		op.ArtifactTechID, op.ArtifactVersion = a.TechID, a.Version // cache techID and version for quick access
		op.ImportState, op.DeployState, op.RequestState =
			lifecycle.ImportNotStarted, lifecycle.DeployNotStarted, lifecycle.RequestPending
	}
	if err := db.Conn().Create(&ops).Error; err != nil {
		return fmt.Errorf("failed to insert artifact tenant operations: %s", err)
	}
	return nil
}
