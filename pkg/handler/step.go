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
	trNumbers := make([]int32, len(step.TransportRequests_V2))
	for i, tr := range step.TransportRequests_V2 {
		trNumbers[i] = int32(tr.ID)
	}
	actionId, err := client.ImportTransportRequest(step.TransportNodeId, trNumbers)
	if err != nil {
		return 0, fmt.Errorf("failed to trigger trs(%v) in node %s(%d): %s", trNumbers, step.TransportNodeName, step.TransportNodeId, err)
	}
	return actionId, nil
}

// all artifacts will be triggered.
// will also update Status, TaskId of these artifacts
func ExecuteDeploy(ctx context.Context, step *db.DeployStep) error {
	var err error
	client, err := cpi.NewClient(ctx, step.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to create cpi client: %s", err)
	}
	for i := range step.Artifacts {
		var taskID string
		artifact := &step.Artifacts[i]
		// currently support two types of artifacts
		if artifact.Type == Artifact_Type_Iflow {
			taskID, err = client.DeployIflow(artifact.Id, "active")
		} else if artifact.Type == Artifact_Type_Sc {
			taskID, err = client.DeployScriptCollection(artifact.Id, "active")
		} else {
			return fmt.Errorf("unsupported artifact type: %s", artifact.Type)
		}
		if err != nil {
			err = fmt.Errorf("error triggering step %d of %s (%s): %s", step.Sequence, artifact.Id, artifact.Type, err)
			continue
		}
		artifact.TaskId = taskID
		artifact.Status = DEPLOY_STATUS_DEPLOYING
	}
	return err
}

// return task ids, task statuses, error
func ExecuteUndeploy(ctx context.Context, step *db.DeployStep) error {
	var err error
	client, err := cpi.NewClient(ctx, step.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to create cpi client: %s", err)
	}

	for i := range step.Artifacts {
		artifact := &step.Artifacts[i]
		if err = client.UndeployRuntimeArtifacts(artifact.Id); err != nil {
			err = fmt.Errorf("error while undeploying %s (%s): %s", artifact.Id, artifact.Type, err)
			artifact.Id = "0"
			artifact.Status = UNDEPLOY_STATUS_FAIL
			continue
		}
		artifact.Id = "1"
		artifact.Status = UNDEPLOY_STATUS_UNDEPLOYING
	}
	return nil
}

// check execution status of running DEPLOY steps then return summary status of the job
// update steps' status to: Error/Running/Success; return job status: Error/Running/Success/Unknown
func CheckDeployJobStatus(steps []db.DeployStep, job db.Job) string {
	jobStatusSet := make(map[string]int, 0)
	for i := range steps { // Running/Error steps should check status
		jobStatusSet[steps[i].Status] = jobStatusSet[steps[i].Status] + 1
	}
	// summarize job status
	jobStatus := mapToJobStatus(jobStatusSet)
	// compared with current job status
	if job.Status == JOB_STATUS_RUNNING && jobStatus == JOB_STATUS_SAVED { // means job just triggered
		jobStatus = job.Status
	}
	return jobStatus
}

// update steps' status to: Error/Running/Success, or warning, initial, unkown.
// return job status: Error/Running/Success/Unknown
func CheckImportJobStatus(steps []db.ImportStep, job db.Job) string {
	jobStatusSet := make(map[string]int, 0)
	for i := range steps {
		jobStatusSet[steps[i].Status] = jobStatusSet[steps[i].Status] + 1
	}
	// update job status
	jobStatus := mapToJobStatus(jobStatusSet)
	// compared with current job status
	if job.Status == JOB_STATUS_RUNNING && jobStatus == JOB_STATUS_SAVED { // means job just triggered
		jobStatus = job.Status
	}
	return jobStatus
}
