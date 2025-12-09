package service

// delivery request related services

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/db"
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

// check tr Number existence in source tenant, and check state is RELEASED
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
	index := -1
	for i, md := range trV1.Content[0].Metadata {
		// NOTE: tms response use Name, not tech ID
		if (md.Name == op.ArtifactTechID || md.Name == op.Artifact.Name) && md.Type == op.Artifact.Type && md.Version == op.ArtifactVersion {
			index = i
			break
		}
	}
	if index == -1 {
		return false, fmt.Errorf("artifact %s, trNumber %s: not match. May use a wrong trNumber for this artifact", op.ArtifactTechID, trNumber)
	}

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

func QueryDrWithAcc(drID uint) (dr *db.DeliveryRequest, err error) {
	if err := db.Conn().
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Preload("ArtifactTenantOperations.Artifact").
		Preload("ArtifactTenantOperations.Tenant").
		First(&dr, drID).Error; err != nil {
		return nil, fmt.Errorf("failed to query delivery request %d: %w", drID, err)
	}
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
	if err := db.Conn().
		Preload("IncludedTenants").
		Preload("ExcludedTenants").
		First(&rule, drRuleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return rule, fmt.Errorf("delivery rule %d not found", drRuleID)
		}
		return rule, fmt.Errorf("failed to get delivery rule %d: %s", drRuleID, err)
	}
	return rule, nil
}

// generate route info for delivery rule. determine source tenant, TMS target routes and nodes
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

func InsertTenantOps(drID uint, ops []db.ArtifactTenantOperation, user string) ([]db.ArtifactTenantOperation, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	sourceTenant, err := SourceTenant(context.Background(), drID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source tenant for delivery request %d: %s", drID, err)
	}
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		a, err := LoadArtifact(*op)
		if err != nil {
			return nil, fmt.Errorf("failed to load artifact for operation %d: %s", op.ID, err)
		}
		// check TR
		if _, err := TrExist(op, sourceTenant); err != nil {
			errOps[op.ID] = fmt.Errorf("transport request check failed for artifact %s: %s", op.ArtifactTechID, err)
			continue
		}
		op.CreatedAt, op.CreatedBy = time.Now(), user
		op.Artifact = a
		op.DeliveryRequestID = drID
		op.ArtifactTechID, op.ArtifactVersion = a.TechID, a.Version // cache techID and version for quick access
		op.ImportState, op.DeployState, op.RequestState =
			lifecycle.ImportNotStarted, lifecycle.DeployNotStarted, lifecycle.RequestPending
	}
	if len(errOps) > 0 {
		errMsg := "errors Occurs while inserting artifact ops:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\t operation %d: %s\n", id, e)
		}
		return nil, errors.New(errMsg)
	}
	if err := db.Conn().Create(&ops).Error; err != nil {
		return nil, fmt.Errorf("failed to insert artifact tenant operations: %s", err)
	}
	return ops, nil
}

// note: this can only update transport request number
func UpdateTenantOps(drID uint, ops []db.ArtifactTenantOperation, user string) ([]db.ArtifactTenantOperation, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	sourceTenant, err := SourceTenant(context.Background(), drID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source tenant for delivery request %d: %s", drID, err)
	}
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		var existingOp db.ArtifactTenantOperation
		if err := db.Conn().First(&existingOp, op.ID).Error; err != nil {
			errOps[op.ID] = fmt.Errorf("failed to find artifact tenant operation %d: %s", op.ID, err)
			continue
		}
		if existingOp.RequestState != lifecycle.RequestPending {
			errOps[op.ID] = fmt.Errorf("cannot update artifact tenant operation %d in state %s. Can only update pending operations", op.ID, existingOp.RequestState)
			continue
		}
		if existingOp.TransportRequestNumber != op.TransportRequestNumber {
			if _, err := TrExist(op, sourceTenant); err != nil {
				errOps[op.ID] = fmt.Errorf("transport request check failed for artifact %s, new %s, old: %s: %s",
					op.ArtifactTechID, op.TransportRequestNumber, existingOp.TransportRequestNumber, err)
				continue
			}
		}
		existingOp.UpdatedBy = user
		existingOp.TransportRequestNumber = op.TransportRequestNumber

		if err := db.Conn().Save(&existingOp).Error; err != nil {
			errOps[op.ID] = fmt.Errorf("failed to update artifact tenant operation %d: %s", op.ID, err)
		}
		op = &existingOp // update back
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during update artifact tenant operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\t operation %d: %s\n", id, e)
		}
		return nil, errors.New(errMsg)
	}
	return ops, nil
}

func SourceTenant(c context.Context, drID uint) (*db.CpiTenant, error) {
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, drID).Error; err != nil {
		return nil, fmt.Errorf("delivery request id %d not found", drID)
	}
	var tenant db.CpiTenant
	if err := db.Conn().First(&tenant, dr.SourceTenantID).Error; err != nil {
		return nil, fmt.Errorf("tenant id %d not found", dr.SourceTenantID)
	}
	return &tenant, nil
}
