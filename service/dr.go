package service

// delivery request related services

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *Service) CreateDeliveryRequest(dr *db.DeliveryRequest) error {
	if err := s.DB.Create(dr).Error; err != nil {
		return fmt.Errorf("failed to create delivery request: %s", err)
	}
	s.publishCounts()
	return nil
}

func (s *Service) DeleteDeliveryRequest(id uint) error {
	if err := s.DB.Delete(&db.DeliveryRequest{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete delivery request %d: %s", id, err)
	}
	s.publishCounts()
	return nil
}

// queryTenantByNodeID queries a CPI tenant by its TMS source node ID
func (s *Service) queryTenantByNodeID(nodeID uint) (*db.CpiTenant, error) {
	var tenant db.CpiTenant
	if err := s.DB.Where(&db.CpiTenant{TmsSourceNodeID: nodeID}).First(&tenant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no cpi tenant found for tms node %d", nodeID)
		}
		return nil, fmt.Errorf("failed to query tenant by node ID %d: %s", nodeID, err)
	}
	return &tenant, nil
}

// query delivery request with all associations
func (s *Service) QueryDrWithAssociations(drID uint) (dr *db.DeliveryRequest, err error) {
	if err := s.DB.
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Preload("ArtifactTenantOperations.Tenant").
		Preload("Conditions", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at asc")
		}).
		First(&dr, drID).Error; err != nil {
		return nil, fmt.Errorf("failed to query delivery request %d: %w", drID, err)
	}
	return
}

// with preloaded Tenant and Artifact
func (s *Service) queryOpsInDrWithAcc(drID uint) (ops []db.ArtifactTenantOperation, err error) {
	if err = s.DB.Where(&db.ArtifactTenantOperation{DeliveryRequestID: drID}).
		Preload("Tenant").
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
func (s *Service) SourceAndRoute(ctx context.Context, includedTenants []db.CpiTenant) (sorceTenant *db.CpiTenant, includedRoutes []db.TransportRoute, includedNodes []db.TransportNode, err error) {
	var transportRoutes []db.TransportRoute
	tmsClient, err := s.TmsSvc(ctx)
	if err != nil {
		return
	}
	if transportRoutes, err = tmsClient.GetRoutes(ctx); err != nil {
		return
	}
	var transportNodes []db.TransportNode
	if transportNodes, err = tmsClient.GetNodes(ctx); err != nil {
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
		nodeIDs[t.TmsSourceNodeID] = true
		includedNodes = append(includedNodes, nodeAll[t.TmsSourceNodeID])
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
		if !targetNodeIDs[t.TmsSourceNodeID] {
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

func (s *Service) GetDeliveryRuleWithAcc(drRuleID uint) (db.DeliveryRule, error) {
	var rule db.DeliveryRule
	if err := s.DB.
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
func (s *Service) GenRouteForRule(ctx context.Context, ruleID uint) (err error) {
	rule, err := s.GetDeliveryRuleWithAcc(ruleID)
	if err != nil {
		return
	}
	sourceTenant, targetRoutes, targetNodes, err := s.SourceAndRoute(ctx, rule.IncludedTenants)
	if err != nil {
		return
	}
	rule.TargetNodes, rule.TargetRoutes = targetNodes, targetRoutes
	rule.SourceTenantID = sourceTenant.ID

	if err := s.DB.Save(&rule).Error; err != nil {
		return err
	}

	return
}

func (s *Service) DeleteTenantOps(drID uint, opIDs []uint) error {
	if len(opIDs) == 0 {
		return nil
	}
	errOps := make(map[uint]error)
	for _, id := range opIDs {
		if id == 0 {
			return fmt.Errorf("invalid operation id: 0")
		}
		var op db.ArtifactTenantOperation
		if err := s.DB.First(&op, id).Error; err != nil {
			errOps[id] = fmt.Errorf("failed to find artifact tenant operation %d. Op may not exists: %s", id, err)
		}
		// check state before delete: allow PENDING and TR_FAILED (no TR created yet)
		if op.RequestState != lifecycle.RequestPending && op.RequestState != lifecycle.RequestTrFailed {
			errOps[id] = fmt.Errorf("cannot delete artifact tenant operation %d in state %s. Can disable delivery", id, op.RequestState)
			continue
		}
		if err := s.DB.Delete(&db.ArtifactTenantOperation{}, id).Error; err != nil {
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
	if drID != 0 {
		s.publishDrOps(drID, s.snapshotOps(drID))
	}
	return nil
}

// resolveTechID queries the CPI PIR OData API for the given package and artifact type,
// then returns the tech ID of the artifact whose display name AND version both match.
//
// Uniqueness rule: if the package contains more than one artifact with the same display
// name (regardless of version), the lookup is rejected as ambiguous. ArtifactName is not
// a unique key in CPI — ArtifactTechID is — but CAS only exposes the display name, making
// it the only available identifier. Duplicate names within a package are therefore treated
// as a naming-convention violation and must be resolved by the user in CPI before delivery.
func (s *Service) resolveTechID(ctx context.Context, cpiCli IntegrationService, packageID, artifactType, displayName, version string) (string, error) {
	type nameID struct{ name, version, id string }
	var items []nameID

	switch strings.ToLower(artifactType) {
	case "iflow", "integrationflow":
		iflows, err := cpiCli.GetPackageIflows(ctx, packageID)
		if err != nil {
			return "", fmt.Errorf("GetPackageIflows(%q): %w", packageID, err)
		}
		for _, f := range iflows {
			items = append(items, nameID{f.Name, f.Version, f.ID})
		}
	case "scriptcollection":
		scs, err := cpiCli.GetPackageScriptcollections(ctx, packageID)
		if err != nil {
			return "", fmt.Errorf("GetPackageScriptcollections(%q): %w", packageID, err)
		}
		for _, sc := range scs {
			items = append(items, nameID{sc.Name, sc.Version, sc.ID})
		}
	default:
		return "", fmt.Errorf("unsupported artifact type %q — only IFlow and ScriptCollection are supported", artifactType)
	}

	// Reject if any other artifact in the package shares the same display name,
	// regardless of version. Duplicate names are ambiguous because CAS cannot
	// return tech IDs — we have no reliable way to distinguish them.
	var sameNameItems []nameID
	for _, it := range items {
		if it.name == displayName {
			sameNameItems = append(sameNameItems, it)
		}
	}
	if len(sameNameItems) > 1 {
		techIDs := make([]string, len(sameNameItems))
		for i, m := range sameNameItems {
			techIDs[i] = m.id
		}
		return "", fmt.Errorf(
			"ambiguous artifact name %q in package %q: found %d artifacts sharing the same display name (tech IDs: %v). "+
				"Please rename the artifacts in CPI to use unique display names within the package.",
			displayName, packageID, len(sameNameItems), techIDs,
		)
	}

	// Find the artifact matching both name and version.
	for _, it := range sameNameItems {
		if it.version == version {
			return it.id, nil
		}
	}
	return "", fmt.Errorf("artifact %q version %q not found in CPI package %q via PIR API", displayName, version, packageID)
}

func (s *Service) InsertTenantOps(ctx context.Context, drID uint, ops []db.ArtifactTenantOperation, user string) ([]db.ArtifactTenantOperation, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	dr, err := s.QueryDrWithAssociations(drID)
	if err != nil {
		return nil, fmt.Errorf("failed to query delivery request %d: %s", drID, err)
	}
	sourceTenant := dr.SourceTenant
	rule, err := s.GetDeliveryRuleWithAcc(dr.DeliveryRuleID)
	if err != nil {
		return nil, fmt.Errorf("failed to get delivery rule %d: %s", dr.DeliveryRuleID, err)
	}

	errOps := make(map[int]error) // keyed by slice index; op.ID is 0 before DB insert
	for i := range ops {
		op := &ops[i]

		if err := s.DeliveryRuleCheck(op, &rule); err != nil {
			errOps[i] = err
			continue
		}

		// check TR — skip when TR is empty (allows auto-created ops from version compare to be saved without TR)
		if op.TransportRequestNumber != "" {
			if _, err := s.TrExist(op, &sourceTenant); err != nil {
				errOps[i] = fmt.Errorf("transport request check failed for artifact %s: %s", op.ArtifactTechID, err)
				continue
			}
		}
		op.CreatedAt, op.CreatedBy = time.Now(), user
		op.DeliveryRequestID = drID
		op.ImportState = lifecycle.ImportNotStarted
		op.RequestState = lifecycle.RequestPending
		if op.SkipDeploy {
			op.DeployState = lifecycle.DeployDisabled
		} else {
			op.DeployState = lifecycle.DeployNotStarted
		}
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during insert artifact tenant operations:\n"
		for i, e := range errOps {
			errMsg += fmt.Sprintf("\t op[%d] %s@%s: %s\n", i, ops[i].ArtifactName, ops[i].ArtifactVersion, e)
		}
		return nil, errors.New(errMsg)
	}
	if err := s.DB.Create(&ops).Error; err != nil {
		return nil, fmt.Errorf("failed to insert artifact tenant operations: %s", err)
	}

	// Fire-and-forget TR generation for source-tenant ops without a pre-populated TR.
	// Ops with a pre-populated TR were already validated by TrExist above — skip CAS.
	// GenerateTransportRequest writes TR state and broadcasts via SSE when done.
	newOpIDs := make([]uint, 0, len(ops))
	for i := range ops {
		if ops[i].TenantID == sourceTenant.ID && ops[i].TransportRequestNumber == "" {
			newOpIDs = append(newOpIDs, ops[i].ID)
		}
	}
	if len(newOpIDs) > 0 {
		go func() {
			s.GenerateTransportRequest(context.Background(), sourceTenant.ID, drID, newOpIDs)
		}()
	}

	return ops, nil
}

// OpUpdateItem carries the only mutable fields for an existing op.
// Artifact identity fields are intentionally absent — they are read from DB.
type OpUpdateItem struct {
	ID                     uint   `json:"ID"`
	TransportRequestNumber string `json:"TransportRequestNumber"`
	SkipDeploy             bool   `json:"SkipDeploy"`
}

func (s *Service) UpdateTenantOps(drID uint, updateItems []OpUpdateItem, user string) ([]db.ArtifactTenantOperation, error) {
	if len(updateItems) == 0 {
		return nil, nil
	}
	dr, err := s.QueryDrWithAssociations(drID)
	if err != nil {
		return nil, fmt.Errorf("failed to query delivery request %d: %s", drID, err)
	}
	sourceTenant := dr.SourceTenant
	errOps := make(map[uint]error)
	var result []db.ArtifactTenantOperation
	for _, item := range updateItems {
		var existingOp db.ArtifactTenantOperation
		if err := s.DB.First(&existingOp, item.ID).Error; err != nil {
			errOps[item.ID] = fmt.Errorf("failed to find artifact tenant operation %d: %s", item.ID, err)
			continue
		}
		if existingOp.RequestState != lifecycle.RequestPending {
			errOps[item.ID] = fmt.Errorf("cannot update artifact tenant operation %d in state %s. Can only update pending operations", item.ID, existingOp.RequestState)
			continue
		}
		if existingOp.TransportRequestNumber != item.TransportRequestNumber {
			// Only validate TR when the new value is non-empty (empty→non-empty or non-empty→different non-empty).
			// Skip when new TR is empty (allows clearing TR or keeping it empty for auto-created ops).
			if item.TransportRequestNumber != "" {
				// Use existingOp's artifact identity for TrExist validation; only the TR number comes from item.
				checkOp := existingOp
				checkOp.TransportRequestNumber = item.TransportRequestNumber
				if _, err := s.TrExist(&checkOp, &sourceTenant); err != nil {
					errOps[item.ID] = fmt.Errorf("transport request check failed for artifact %s, new %s, old: %s: %s",
						existingOp.ArtifactTechID, item.TransportRequestNumber, existingOp.TransportRequestNumber, err)
					continue
				}
			}
		}
		existingOp.UpdatedBy = user
		existingOp.TransportRequestNumber = item.TransportRequestNumber
		existingOp.SkipDeploy = item.SkipDeploy
		if item.SkipDeploy {
			existingOp.DeployState = lifecycle.DeployDisabled
		} else {
			existingOp.DeployState = lifecycle.DeployNotStarted
		}
		if err := s.DB.Save(&existingOp).Error; err != nil {
			errOps[item.ID] = fmt.Errorf("failed to update artifact tenant operation %d: %s", item.ID, err)
			continue
		}
		result = append(result, existingOp)
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during update artifact tenant operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\t operation %d: %s\n", id, e)
		}
		return nil, errors.New(errMsg)
	}
	s.publishDrOps(drID, s.snapshotOps(drID))
	return result, nil
}

func (s *Service) BatchInsertConditions(conditions []db.Condition) error {
	if len(conditions) == 0 {
		return nil
	}
	if err := s.DB.Create(&conditions).Error; err != nil {
		return fmt.Errorf("error when inserting conditions: %w", err)
	}
	return nil
}
