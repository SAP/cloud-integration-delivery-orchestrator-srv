package service

import (
	"context"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
)

// delivery request related services

// check tr Number existence and origin
func TrExist(ops []db.ArtifactTenantOperation, sourceTenant *db.CpiTenant) (bool, error) {
	for i := range ops {
		op := &ops[i]
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

	}
	return true, nil
}

// check and load artifact info into db, set ArtifactID in ops
func LoadArtifact(op db.ArtifactTenantOperation) (artifactID uint, err error) {
	a := &op.Artifact
	if db.Conn().FirstOrCreate(a, &db.Artifact{TechID: a.TechID, Version: a.Version}).Error != nil {
		err = fmt.Errorf("error when saving artifact %s:%s", a.TechID, a.Version)
		return
	}
	artifactID = a.ID
	return
}

// when call this function, make sure all ops have valid tr number
func SyncImportState(deliveryRequestID uint) ([]db.ArtifactTenantOperation, error) {
	var artifactOps []db.ArtifactTenantOperation
	// Adjust the DB accessor (db.DB / db.GetDB()) to match your project setup
	if err := db.Conn().Where(&db.ArtifactTenantOperation{DeliveryRequestID: deliveryRequestID}).Preload("Tenant").Find(&artifactOps).Error; err != nil {
		return nil, fmt.Errorf("db query failed: %w", err)
	}
	if len(artifactOps) == 0 {
		return []db.ArtifactTenantOperation{}, nil
	}
	tmsClient, err := tms.NewClient(context.Background())
	if err != nil {
		return nil, fmt.Errorf("error creating tms client: %w", err)
	}
	trStatus := make(map[string]map[uint]tms.TrNodeStatus)              // tr number status in all nodes. trNumber - map[nodeID]status
	tenantToOps := make(map[uint]map[string]db.ArtifactTenantOperation) // arTenantOp record in each node. cpi tenant ID - map[trNumber]ArtifactTenantOperation
	//
	for _, op := range artifactOps {
		trNumber := op.TransportRequestNumber
		if _, ok := trStatus[trNumber]; ok { // already fetched
			continue
		}
		// UpdateArtifactNodeStatus will call GetTransportRequest internally
		ns, err := tmsClient.TrNodeStatuses(trNumber)
		if err != nil {
			return nil, fmt.Errorf("error when getting transport request %s: %w", trNumber, err)
		}
		trStatus[trNumber] = ns
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

	nodetoTenantID := nodeToTenant() // tms node ID - cpi tenant ID
	for _, op := range artifactOps { // check to create new record if new tr status happens in tms
		trNumber := op.TransportRequestNumber
		// same artifactOp == same trNumber, but in diffrent tms nods
		for nID, nState := range trStatus[trNumber] {
			tenantID := nodetoTenantID[nID]
			if _, ok := tenantToOps[tenantID]; !ok {
				tenantToOps[tenantID] = make(map[string]db.ArtifactTenantOperation)
			}
			if _, ok := tenantToOps[tenantID][trNumber]; !ok { // means this a new status happens in tms, should create a new record
				newOp := db.ArtifactTenantOperation{
					DeliveryRequestID:      op.DeliveryRequestID,
					ArtifactID:             op.ArtifactID,
					ArtifactTechID:         op.ArtifactTechID,
					ArtifactVersion:        op.ArtifactVersion,
					TenantID:               tenantID, // TODO: map nID to tenantID
					TransportRequestNumber: trNumber,
					ImportState:            lifecycle.DeriveImport(nState.Status),
					DeployState:            lifecycle.DeployNotStarted,
				}
				tenantToOps[tenantID][trNumber] = newOp
			}
			curOp := tenantToOps[tenantID][trNumber]
			curOp.ImportState = lifecycle.DeriveImport(nState.Status)
			if err := db.Conn().Save(&curOp).Error; err != nil {
				return nil, fmt.Errorf("error when creating new artifact tenant operation for artifact %s in node %d: %w", curOp.ArtifactTechID, nID, err)
			}

		}

	}
	return artifactOps, nil
}

func GenRoute(sourceTenant db.CpiTenant) (targetRoutes []db.TransportRoute, targetNodes []db.TransportNode, err error) {
	// generate Transport routes
	var tmsCli *tms.TmsClient
	tmsCli, err = tms.NewClient(context.Background())
	if err != nil {
		return
	}
	var transportRoutes []db.TransportRoute
	transportRoutes, err = tmsCli.GetRoutes()
	if err != nil {
		return
	}
	var transportNodes []db.TransportNode
	transportNodes, err = tmsCli.GetNodes()
	if err != nil {
		return
	}

	targetRoutes, targetNodes = downstreamfromSource(sourceTenant.TransportNodeID, transportNodes, transportRoutes)
	return
}

func ScheduleImport(drID uint, targetNode uint) (bool, error) {
	var ops []db.ArtifactTenantOperation
	if err := db.Conn().Preload("Artifact").
		Where(&db.ArtifactTenantOperation{DeliveryRequestID: drID}).
		Find(&ops).Error; err != nil {
		return false, fmt.Errorf("failed to query artifact operations for delivery request %d: %s", drID, err)
	}
	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, drID).Error; err != nil {
		return false, fmt.Errorf("failed to query delivery request %d: %s", drID, err)
	}

	if len(ops) == 0 {
		return false, fmt.Errorf("no artifact operations found for delivery request %d", drID)
	}

	trs := make([]uint, 0)
	for i := range ops {
		op := &ops[i]
		if op.ImportState != lifecycle.ImportQueued || op.Tenant.TransportNodeID != targetNode { // only queued(INITIAL) state can be triggered for import
			continue
		}
		op.ImportState = lifecycle.ImportInProgress
		trNumber, err := ToUint(op.TransportRequestNumber)

		if err != nil {
			return false, fmt.Errorf("invalid transport request number %s for artifact operation %d: %s", op.TransportRequestNumber, op.ID, err)
		}
		trs = append(trs, trNumber)
	}
	tmsCli, err := tms.NewClient(context.Background())
	if err != nil {
		return false, err
	}
	if _, err := tmsCli.ImportTransportRequest(targetNode, trs); err != nil {
		return false, err
	}
	// update import state in db
	for i := range ops {
		op := &ops[i]
		if err := db.Conn().Model(op).Updates(op).Error; err != nil {
			return false, fmt.Errorf("failed to update artifact operation %d: %s", op.ID, err)
		}
	}

	return true, nil
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

// TODO: cache this mapping, or load when service starts
func nodeToTenant() map[uint]uint {
	var tenants []db.CpiTenant
	if err := db.Conn().Find(&tenants).Error; err != nil {
		return nil
	}
	mapping := make(map[uint]uint)
	for _, t := range tenants {
		mapping[t.TransportNodeID] = t.ID
	}
	return mapping
}
