package service

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/tms"

	"gorm.io/gorm"
)

// import INITIAL artifact operations under target node
func BatchImportTenantOps(opIDs []uint, targetTenantID uint, user string) (bool, error) {
	var ops []db.ArtifactTenantOperation
	var err error
	if ops, err = queryOpsWithAcco(opIDs); err != nil {
		return false, err
	}

	var tenant *db.CpiTenant
	if tenant, err = queryTenant(targetTenantID); err != nil {
		return false, err
	}
	targetNodeID := tenant.TransportNodeID

	trs := make([]uint, 0)
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		if op.ImportState != ImportQueued || op.Tenant.TransportNodeID != targetNodeID { // only queued(INITIAL) state can be triggered for import
			// TODO: may create a Condition for these check failures
			errOps[op.ID] = fmt.Errorf("cannot import artifact operation #%d(at TMS node %d) for target TMS node %d. Import State: %s", op.ID, op.Tenant.TransportNodeID, targetNodeID, op.ImportState)
			continue
		}
		op.ImportState = ImportInProgress
		trNumber, err := ToUint(op.TransportRequestNumber)
		// TODO: EVERY IMPORTANT! validate if there is version decrease in target tenant before import

		if err != nil {
			errOps[op.ID] = fmt.Errorf("invalid transport request number %s for artifact operation %d: %s", op.TransportRequestNumber, op.ID, err)
			continue
		}
		trs = append(trs, trNumber)
		op.UpdatedBy = user
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during preparing import operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\t operation #%d: %s\n", id, e)
		}
		return false, errors.New(errMsg)
	}
	tmsCli, err := tms.NewClient(context.Background())
	if err != nil {
		return false, fmt.Errorf("error when creating tms client: %s", err)
	}
	if _, err := tmsCli.ImportTransportRequest(targetNodeID, trs); err != nil {
		return false, err
	}
	err = batchUpdateOps(ops)
	if err != nil {
		return false, err
	}
	return true, nil
}

// when trggered deploy, the artifact operation will be set to DeployInProgress
func BatchDeployTenantOps(opIDs []uint, targetTenantID uint, user string) (bool, error) {
	var ops []db.ArtifactTenantOperation
	var err error
	if ops, err = queryOpsWithAcco(opIDs); err != nil {
		return false, err
	}
	var tenant *db.CpiTenant
	if tenant, err = queryTenant(targetTenantID); err != nil { // check tenant existence
		return false, err
	}
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		if op.DeployState != DeployQueued { // deploy should be QUEUED. i.e.,
			continue
		}
		if op.Tenant.ID != targetTenantID { // only queued state can be triggered for deploy
			// TODO: raise error when tenantID not match
			errOps[op.ID] = fmt.Errorf("artifact operation %d not match target tenant %s#%d", op.ID, tenant.Name, tenant.ID)
			continue
		}
		cpiCli, err := cpi.NewClient(context.Background(), op.Tenant.CpiEndpoint.Name)
		if err != nil {
			errOps[op.ID] = fmt.Errorf("failed to create cpi client for tenant %s: %s", op.Tenant.Name, err)
			continue
		}
		_, err = cpiCli.DeployArtifact(op.ArtifactTechID, op.ArtifactVersion, op.Artifact.Type)
		if err != nil {
			errOps[op.ID] = fmt.Errorf("failed to deploy artifact %s:%s to tenant %s: %s", op.ArtifactTechID, op.ArtifactVersion, op.Tenant.Name, err)
			continue
		}
		op.DeployState = DeployInProgress
		op.UpdatedBy = user
		if err := db.Conn().Model(op).Updates(op).Error; err != nil {
			errOps[op.ID] = fmt.Errorf("failed to update artifact operation %d: %s", op.ID, err)
			continue
		}
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during deploy operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\t operation %d: %s\n", id, e)
		}
		return false, errors.New(errMsg)
	}
	return true, nil
}

func queryTenant(tenantID uint) (*db.CpiTenant, error) {
	var tenant db.CpiTenant
	if err := db.Conn().First(&tenant, tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("tenant %d not found", tenantID)
		}
		return nil, fmt.Errorf("failed to query tenant %d: %s", tenantID, err)
	}
	return &tenant, nil
}

// query artifact operations with preloaded Artifact and Tenant
func queryOpsWithAcco(opIDs []uint) ([]db.ArtifactTenantOperation, error) {
	if len(opIDs) == 0 {
		return nil, fmt.Errorf("no operation ids provided")
	}

	var ops []db.ArtifactTenantOperation
	if err := db.Conn().Preload("Artifact").Preload("Tenant").
		Find(&ops, opIDs).Error; err != nil {
		return nil, fmt.Errorf("failed to query artifact operations of %v: %s", opIDs, err)
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no artifact operations found for ids %v", opIDs)
	}

	found := make(map[uint]bool, len(ops))
	for _, o := range ops {
		found[o.ID] = true
	}
	var missing []uint
	for _, id := range opIDs {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("artifact operations not found for ids: %v", missing)
	}
	return ops, nil
}

func batchUpdateOps(ops []db.ArtifactTenantOperation) error {
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		if err := db.Conn().Model(op).Updates(op).Error; err != nil {
			errOps[op.ID] = fmt.Errorf("failed to update artifact operation %d: %s", op.ID, err)
			continue
		}
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during batch update operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\t operation %d: %s\n", id, e)
		}
		return errors.New(errMsg)
	}
	return nil
}
