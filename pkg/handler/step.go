package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

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
func CheckDeployStatus(ctx context.Context, steps []db.DeployStep) (string, error) {
	jobStatusSet := make(map[string]int, 0)
	for i := range steps { // Running/Error steps should check status
		step := &steps[i]
		if step.Status == STEP_STATUS_SUCCESS || step.Status == STEP_STATUS_SAVED || len(step.TaskIds) == 0 { // taskids is 0/nil means deploying has not been triggerd yet
			jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1
			continue
		}
		statusSet := make(map[string]bool, 0)
		// for deploy step, each artifact as a delpoy task id by using task ids.
		// if it is a undeploy step, check status of each artifact by using artifact ids.
		for j, taskId := range step.TaskIds {
			// possible statuses of CPI response: Success, Fail, Deploying, Fail_On_License_Error
			if step.TaskStatuses[j] == DEPLOY_STATUS_SUCCESS { // skip already finished tasks
				continue
			}
			cpiClient, err := cpi.NewClient(ctx, step.Endpoint) // check task status via cpi endpoint
			if err != nil {
				return "", fmt.Errorf("failed to connect to cpi endpoint: %s: %s", step.Endpoint, err)
			}
			var status string // deploy/undeploy stautus of a step
			if step.Type == "Undeploy" {
				status, err = cpiClient.CheckUndeployStatus(step.ArtifactIds[j])
			} else if step.Type == "Deploy" {
				status, err = cpiClient.CheckDeployStatus(taskId)
			}
			if err != nil {
				return "", fmt.Errorf("error while checking status of step %d, type: %s, artifactId: %s, taskId: %s: %s", i, step.Type, step.ArtifactIds[j], taskId, err)
			}
			step.TaskStatuses[j] = status
			statusSet[status] = true
		}
		// map step status to Error/Running/Success
		if statusSet[DEPLOY_STATUS_FAIL] || statusSet[DEPLOY_STATUS_FAIL_ON_LICENSE_ERROR] {
			step.Status = STEP_STATUS_ERROR
		} else if statusSet[DEPLOY_STATUS_DEPLOYING] || statusSet[UNDEPLOY_STATUS_UNDEPLOYING] {
			step.Status = STEP_STATUS_RUNNING
		} else {
			step.Status = STEP_STATUS_SUCCESS
			// TODO: not sure if there is any approches to get the exact end time in CPI response
			step.EndedAt = time.Now()
		}

		jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1 // recode status count
		if err := db.Conn().Model(&step).Updates(step).Error; err != nil {
			return "", fmt.Errorf("error while updating status of step %d: %s", i, err)
		}
	}
	// summarize job status
	jobStatus := mapToJobStatus(jobStatusSet)
	return jobStatus, nil
}

// update steps' status to: Error/Running/Success, or warning, initial, unkown.
// return job status: Error/Running/Success/Unknown
func CheckImportStatus(ctx context.Context, steps []db.ImportStep) (string, error) {
	tmsClient, err := tms.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to connect to tms: %s", err)
	}
	jobStatusSet := make(map[string]int, 0)
	for i := range steps {
		step := &steps[i]
		if step.Status == STEP_STATUS_SUCCESS || step.Status == STEP_STATUS_SAVED || step.ActionId == 0 {
			jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1
			continue
		}
		// possible statuses of tms action: succeeded, warning, error, fatal, running, initial, unknown
		status, endedAt, err := tmsClient.GetActionResult(step.ActionId)
		if err != nil {
			return "", fmt.Errorf("error while getting status of action id %d of step %d", step.ActionId, i)
		}
		// update status, map to step status Success/Error/Running
		if status == "succeeded" {
			status = STEP_STATUS_SUCCESS
		} else if status == "error" || status == "fatal" {
			status = STEP_STATUS_ERROR
		} else if status == "running" {
			status = STEP_STATUS_RUNNING
		}
		// or use TMS status directly: warning, initial, unknown
		step.Status = status
		step.EndedAt, _ = time.Parse(time.RFC3339, endedAt)
		jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1
		if err := db.Conn().Model(&step).Updates(step).Error; err != nil {
			return "", fmt.Errorf("error while updating status of step %d (actionId: %d): %s", i, step.ActionId, err)
		}
	}
	// update job status
	jobStatus := mapToJobStatus(jobStatusSet)
	return jobStatus, nil
}
