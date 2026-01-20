package service

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"
	"mmt-delivery/pkg/xsuaa"
	"strings"

	"gorm.io/gorm"
)

// import INITIAL artifact operations under target node
func BatchImportTenantOps(drID uint, opIDs []uint, targetTenantID uint, userID string) (bool, error) {
	var ops []db.ArtifactTenantOperation
	var err error
	if ops, err = queryOpsWithAcco(opIDs); err != nil {
		return false, err
	}

	var targetTenant *db.CpiTenant
	if targetTenant, err = queryTenant(targetTenantID); err != nil {
		return false, err
	}
	targetNodeID := targetTenant.TransportNodeID

	trs := make([]uint, 0)
	errOps := make(map[uint]error)
	// pre-check before import
	for i := range ops {
		op := &ops[i]
		// only queued(INITIAL), Failed(enable re-import) state can be triggered for import
		if (op.ImportState != lifecycle.ImportQueued && op.ImportState != lifecycle.ImportFailed) || op.Tenant.TransportNodeID != targetNodeID {
			errOps[op.ID] = fmt.Errorf("cannot import artifact operation #%d(at TMS node %d) for target TMS node %d. Import State: %s", op.ID, op.Tenant.TransportNodeID, targetNodeID, op.ImportState)
			continue
		}
		trNumber, err := ToUint(op.TransportRequestNumber)
		if err != nil {
			errOps[op.ID] = fmt.Errorf("invalid transport request number %s for artifact operation %d: %s", op.TransportRequestNumber, op.ID, err)
			continue
		}
		// NOTE: VERY IMPORTANT! validate if there is version decrease in target tenant before import
		if err := checkVersionDowngradeInTenant(op, targetTenant); err != nil {
			errOps[op.ID] = err
			continue
		}
		trs = append(trs, trNumber)
		op.ImportState = lifecycle.ImportInProgress
		op.UpdatedBy = userID
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during preparing import operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\toperation #%d: %s\n", id, e)
		}
		return false, errors.New(errMsg)
	}
	// update ops state to InProgress first
	err = batchUpdateOps(ops)
	if err != nil {
		return false, err
	}

	// trigger async import in goroutine to avoid blocking
	go func(drID uint, targetNodeID uint, targetTenantName string, trs []uint, ops []db.ArtifactTenantOperation, user string) {
		tmsCli, err := tms.NewClient(context.Background())
		if err != nil {
			// revert ops state to ImportFailed on TMS client creation error
			for i := range ops {
				ops[i].ImportState = lifecycle.ImportFailed
			}
			_ = batchUpdateOps(ops)

			condition := db.Condition{
				DeliveryRequestID: drID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("batch import failed to create TMS client for tenant %s (node %d). Error: %s", targetTenantName, targetNodeID, err.Error()),
			}
			BatchInsertConditions([]db.Condition{condition})
			return
		}

		actionID, err := tmsCli.ImportTransportRequest(targetNodeID, trs)
		if err != nil {
			// revert ops state to ImportFailed on import error
			for i := range ops {
				ops[i].ImportState = lifecycle.ImportFailed
			}
			_ = batchUpdateOps(ops)

			condition := db.Condition{
				DeliveryRequestID: drID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("batch import failed in tenant %s (node %d). Error: %s", targetTenantName, targetNodeID, err.Error()),
			}
			BatchInsertConditions([]db.Condition{condition})
			return
		}

		// save condition if import succeeded
		artifactList := make([]string, 0, len(ops))
		for _, op := range ops {
			artifactList = append(artifactList, fmt.Sprintf("  - %s (version %s)", op.ArtifactTechID, op.ArtifactVersion))
		}
		userEmail, _ := xsuaa.GetUserEmail(user)
		condition := db.Condition{
			DeliveryRequestID: drID,
			State:             lifecycle.CondSuccess,
			Message:           fmt.Sprintf("batch import triggered in tenant %s (node %d) by %s. Action ID: %d.\nArtifacts:\n%s", targetTenantName, targetNodeID, userEmail, actionID, strings.Join(artifactList, "\n")),
		}
		BatchInsertConditions([]db.Condition{condition})
	}(drID, targetNodeID, targetTenant.Name, trs, ops, userID)

	return true, nil
}

// when trggered deploy, the artifact operation will be set to DeployInProgress
func BatchDeployTenantOps(drID uint, opIDs []uint, targetTenantID uint, userID string) (bool, error) {
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
	validOps := make([]db.ArtifactTenantOperation, 0)
	// pre-check before deploy
	for i := range ops {
		op := &ops[i]
		if op.DeployState != lifecycle.DeployQueued && op.DeployState != lifecycle.DeployFailed { // deploy should be QUEUED. i.e.,
			errOps[op.ID] = fmt.Errorf("artifact operation %d is not in QUEUED/FAILED state for deploy, current state: %s", op.ID, op.DeployState)
			continue
		}
		if op.Tenant.ID != targetTenantID {
			errOps[op.ID] = fmt.Errorf("artifact operation %d not match target tenant %s#%d", op.ID, tenant.Name, tenant.ID)
			continue
		}
		op.DeployState = lifecycle.DeployInProgress
		op.UpdatedBy = userID
		validOps = append(validOps, *op)
	}
	if len(errOps) > 0 {
		errMsg := "errors occurred during preparing deploy operations:\n"
		for id, e := range errOps {
			errMsg += fmt.Sprintf("\toperation #%d: %s\n", id, e)
		}
		return false, errors.New(errMsg)
	}

	// update ops state to InProgress
	err = batchUpdateOps(validOps)
	if err != nil {
		return false, err
	}

	// trigger async deploy in goroutine to avoid blocking
	go func(drID uint, tenant *db.CpiTenant, ops []db.ArtifactTenantOperation, user string) {
		errOps := make(map[uint]error)
		successOps := make([]db.ArtifactTenantOperation, 0)
		failedOps := make([]db.ArtifactTenantOperation, 0)

		for i := range ops {
			op := &ops[i]
			cpiCli, err := cpi.NewClient(context.Background(), op.Tenant.CpiEndpoint.Name)
			if err != nil {
				errOps[op.ID] = fmt.Errorf("failed to create cpi client for tenant %s: %s", op.Tenant.Name, err)
				// mark as failed and continue
				op.DeployState = lifecycle.DeployFailed
				failedOps = append(failedOps, *op)
				continue
			}
			_, err = cpiCli.DeployArtifact(op.ArtifactTechID, op.ArtifactVersion, op.Artifact.Type)
			if err != nil {
				errOps[op.ID] = fmt.Errorf("failed to deploy artifact %s:%s to tenant %s: %s", op.ArtifactTechID, op.ArtifactVersion, op.Tenant.Name, err)
				// mark as failed and continue
				op.DeployState = lifecycle.DeployFailed
				failedOps = append(failedOps, *op)
				continue
			}
			successOps = append(successOps, *op)
		}

		// update failed ops state to DeployFailed in database
		if len(failedOps) > 0 {
			_ = batchUpdateOps(failedOps)
		}

		// record conditions based on results
		if len(errOps) > 0 {
			errMsg := "errors occurred during async deploy operations:\n"
			for id, e := range errOps {
				errMsg += fmt.Sprintf("\toperation %d: %s\n", id, e)
			}
			condition := db.Condition{
				DeliveryRequestID: drID,
				State:             lifecycle.CondError,
				Message:           fmt.Sprintf("batch deploy failed in tenant %s. Error: %s", tenant.Name, errMsg),
			}
			BatchInsertConditions([]db.Condition{condition})
		}

		// save condition for successful deployments
		if len(successOps) > 0 {
			artifactList := make([]string, 0, len(successOps))
			for _, op := range successOps {
				artifactList = append(artifactList, fmt.Sprintf("  - %s (version %s)", op.ArtifactTechID, op.ArtifactVersion))
			}
			userEmail, _ := xsuaa.GetUserEmail(user)
			condition := db.Condition{
				DeliveryRequestID: drID,
				State:             lifecycle.CondSuccess,
				Message:           fmt.Sprintf("batch deploy triggered in tenant %s by %s. Artifacts:\n%s", tenant.Name, userEmail, strings.Join(artifactList, "\n")),
			}
			BatchInsertConditions([]db.Condition{condition})
		}
	}(drID, tenant, validOps, userID)

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
