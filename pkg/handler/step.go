package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
)

func DeleteStep(ctx *gin.Context) {
	stepId, err := strconv.Atoi(ctx.Query("id"))
	stepType := ctx.Query("type")
	if err != nil || stepType == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("bad request. step id:%s, step type: %s", err, stepType),
		})
		return
	}

	models := map[string]any{
		"Import":   &db.ImportStep{},
		"Deploy":   &db.DeployStep{},
		"Undeploy": nil,
	}

	if err := db.Conn().Delete(models[stepType], stepId).Error; err != nil {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": stepId,
	})
}

// returns job id
func ExecuteImport(ctx context.Context, step db.ImportStep) (uint, error) {
	client, err := tms.NewClient(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to create tms client: %s", err)
	}
	actionId, err := client.ImportTransportRequest(step.TransportNodeId, step.TransportRequests)
	if err != nil {
		return 0, fmt.Errorf("failed to trigger trs(%v) in node %s(%d): %s", step.TransportRequests, step.TransportNodeName, step.TransportNodeId, err)
	}
	return actionId, nil
}

// returns taskIds, taskStatuses, err
// all artifacts will be triggered.
func ExecuteDeploy(ctx context.Context, step db.DeployStep) ([]string, []string, error) {
	taskIds := make([]string, len(step.ArtifactIds))
	taskStatuses := make([]string, len(step.ArtifactIds))
	var err error
	client, err := cpi.NewClient(ctx, step.Endpoint)
	if err != nil {
		return taskIds, taskStatuses, err
	}
	for i, artifactId := range step.ArtifactIds {
		var actionID string
		// currently support two types of artifacts
		if step.ArtifactTypes[i] == "Integration Flow" {
			actionID, err = client.DeployIflow(artifactId, "active")
		} else if step.ArtifactTypes[i] == "Script Collection" {
			actionID, err = client.DeployScriptCollection(artifactId, "active")
		}
		if err != nil {
			err = fmt.Errorf("error triggering step %d of %s (%s): %s", step.Sequence, artifactId, step.ArtifactTypes[i], err)
			taskStatuses[i] = err.Error()
			taskIds[i] = "0"
			continue
		}
		taskStatuses[i] = DEPLOY_STATUS_DEPLOYING
		taskIds[i] = actionID
	}
	return taskIds, taskStatuses, err
}

// return task ids, task statuses, error
func ExecuteUndeploy(ctx context.Context, step db.DeployStep) ([]string, []string, error) {
	taskIds := make([]string, len(step.ArtifactIds))
	taskStatuses := make([]string, len(step.ArtifactIds))
	var err error
	client, err := cpi.NewClient(ctx, step.Endpoint)
	if err != nil {
		return taskIds, taskStatuses, fmt.Errorf("failed to create cpi client: %s", err)
	}

	for i, artifactId := range step.ArtifactIds {
		if err = client.UndeployRuntimeArtifacts(artifactId); err != nil {
			err = fmt.Errorf("error while undeploying %s (%s): %s", artifactId, step.ArtifactTypes[i], err)
			taskStatuses[i] = err.Error()
			taskIds[i] = "0"
			continue
		}
		taskIds[i] = "1"
		taskStatuses[i] = UNDEPLOY_STATUS_UNDEPLOYING
	}
	return taskIds, taskStatuses, nil
}

// check execution status of running DEPLOY steps then return summary status of the job
// update steps' status to: Error/Running/Success; return job status: Error/Running/Success/Unknown
func CheckDeployJobStatus(steps []db.DeployStep) (string, error) {
	jobStatusSet := make(map[string]int, 0)
	for i := range steps { // Running/Error steps should check status
		jobStatusSet[steps[i].Status] = jobStatusSet[steps[i].Status] + 1
	}
	// summarize job status
	jobStatus := mapToJobStatus(jobStatusSet)
	return jobStatus, nil
}

// update steps' status to: Error/Running/Success, or warning, initial, unkown.
// return job status: Error/Running/Success/Unknown
func CheckImportJobStatus(steps []db.ImportStep) (string, error) {
	jobStatusSet := make(map[string]int, 0)
	for i := range steps {
		jobStatusSet[steps[i].Status] = jobStatusSet[steps[i].Status] + 1
	}
	// update job status
	jobStatus := mapToJobStatus(jobStatusSet)
	return jobStatus, nil
}
