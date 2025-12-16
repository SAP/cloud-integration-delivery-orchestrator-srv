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

// map tms node ID -> cpi tenant ID
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

// query delivery request with all associations
func QueryDrWithAssociations(drID uint) (dr *db.DeliveryRequest, err error) {
	if err := db.Conn().
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Preload("ArtifactTenantOperations.Artifact").
		Preload("ArtifactTenantOperations.Tenant").
		Preload("Conditions").
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

func GetDeliveryRuleWithAcc(drRuleID uint) (db.DeliveryRule, error) {
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
	rule, err := GetDeliveryRuleWithAcc(ruleID)
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
			return fmt.Errorf("invalid operation id: 0")
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
	dr, err := QueryDrWithAssociations(drID)
	if err != nil {
		return nil, fmt.Errorf("failed to query delivery request %d: %s", drID, err)
	}
	sourceTenant := dr.SourceTenant
	rule, err := GetDeliveryRuleWithAcc(dr.DeliveryRuleID)
	if err != nil {
		return nil, fmt.Errorf("failed to get delivery rule %d: %s", dr.DeliveryRuleID, err)
	}
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		a, err := LoadArtifact(*op)
		if err != nil {
			return nil, fmt.Errorf("failed to load artifact for operation %d: %s", op.ID, err)
		}

		if err := deliveryRuleCheck(op, &rule); err != nil {
			errOps[op.ID] = err
			continue
		}
		// check TR
		if _, err := TrExist(op, &sourceTenant); err != nil {
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
		errMsg := "errors occurred during insert artifact tenant operations:\n"
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

// NOTE: can only update transport request number
func UpdateTenantOps(drID uint, ops []db.ArtifactTenantOperation, user string) ([]db.ArtifactTenantOperation, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	dr, err := QueryDrWithAssociations(drID)
	if err != nil {
		return nil, fmt.Errorf("failed to query delivery request %d: %s", drID, err)
	}
	sourceTenant := dr.SourceTenant
	errOps := make(map[uint]error)
	for i := range ops {
		draftOp := &ops[i]
		var existingOp db.ArtifactTenantOperation
		if err := db.Conn().First(&existingOp, draftOp.ID).Error; err != nil {
			errOps[draftOp.ID] = fmt.Errorf("failed to find artifact tenant operation %d: %s", draftOp.ID, err)
			continue
		}
		if existingOp.RequestState != lifecycle.RequestPending {
			errOps[draftOp.ID] = fmt.Errorf("cannot update artifact tenant operation %d in state %s. Can only update pending operations", draftOp.ID, existingOp.RequestState)
			continue
		}
		if existingOp.TransportRequestNumber != draftOp.TransportRequestNumber {
			if _, err := TrExist(draftOp, &sourceTenant); err != nil {
				errOps[draftOp.ID] = fmt.Errorf("transport request check failed for artifact %s, new %s, old: %s: %s",
					draftOp.ArtifactTechID, draftOp.TransportRequestNumber, existingOp.TransportRequestNumber, err)
				continue
			}
		}
		existingOp.UpdatedBy = user
		existingOp.TransportRequestNumber = draftOp.TransportRequestNumber

		if err := db.Conn().Save(&existingOp).Error; err != nil {
			errOps[draftOp.ID] = fmt.Errorf("failed to update artifact tenant operation %d: %s", draftOp.ID, err)
		}
		draftOp = &existingOp // update back
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

func BatchInsertConditions(conditions []db.Condition) error {
	if len(conditions) == 0 {
		return nil
	}
	if err := db.Conn().Create(&conditions).Error; err != nil {
		return fmt.Errorf("error when inserting conditions: %w", err)
	}
	return nil
}
