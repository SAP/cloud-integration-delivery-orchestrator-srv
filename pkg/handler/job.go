package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	// "time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/mitchellh/mapstructure"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
)

// Get job detail with step list, by jobid
func GetJobAndStepsByID(ctx *gin.Context) {
	jobId, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("invalid job id %d: %s", jobId, err),
		})
		return
	}
	var job db.Job
	if err := db.Conn().First(&job, jobId).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("error while querying job %d: %s", jobId, err),
		})
		return
	}
	var steps interface{}
	jobStatus := job.Status

	if job.Type == "Import" {
		var importSteps []db.ImportStep
		var err error
		if err = db.Conn().Where(db.ImportStep{JobId: job.ID}).Find(&importSteps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError,
				gin.H{"msg": fmt.Sprintf("error while querying Import Steps of job %d: %s", job.ID, err)},
			)
			return
		}
		if jobStatus, err = checkImportStatus(ctx, importSteps); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while updating status of steps: %s", err),
			})
			return
		}
		steps = importSteps
	} else if job.Type == "Deploy" {
		var deploySteps []db.DeployStep
		var err error
		if err := db.Conn().Where(db.DeployStep{JobId: job.ID}).Find(&deploySteps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError,
				gin.H{"msg": fmt.Sprintf("error while querying Deploy Steps of job %d: %s", job.ID, err)},
			)
			return
		}
		if jobStatus, err = checkDeployStatus(ctx, deploySteps); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("error while checking deploying status: %s", err)})
			return
		}
		steps = deploySteps
	} else if job.Type == "Undepoloy" {
		// todo
	} else {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("invalid job type: %s", job.Type),
		})
		return
	}

	var jobLogs []db.ExecutionLog
	if err := db.Conn().Where(db.ExecutionLog{JobId: job.ID}).Find(&jobLogs).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("error while querying logs of job %d: %s", job.ID, err),
		})
		return
	}

	db.Conn().Model(&job).Updates(db.Job{Status: jobStatus})

	ctx.JSON(http.StatusOK, gin.H{
		"result": struct {
			db.Job
			Steps         any               `json:"Steps"`
			ExecutionLogs []db.ExecutionLog `json:"ExecutionLogs"`
		}{
			job,
			steps,
			jobLogs,
		},
	})
}

func CreateJob(ctx *gin.Context) {
	var job db.Job = db.Job{}
	if err := ctx.BindJSON(&job); err != nil {
		logger.Errorf("invalid request, error message is %s", err)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("invalid request: %s", err),
		})
		return
	}
	job.Status = "Submitted"
	job.CreatedBy = user(ctx)

	if err := db.Conn().Save(&job).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": "error while creating job",
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": job,
	})
}

func GetJobsByType(ctx *gin.Context) {
	job_type := ctx.Query("type")

	var jobs []db.Job

	if err := db.Conn().Where(map[string]interface{}{"type": job_type}).Find(&jobs).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": err,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": jobs,
	})
}

// Delete job and steps within it
func DeleteJob(ctx *gin.Context) {
	id := ctx.Param("id")
	if id == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": "no id available, please specify the id of config",
		})
		return
	}
	jobId, _ := strconv.Atoi(id)

	if err := db.Conn().Delete(&db.Job{}, jobId).Error; err != nil {
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"result": jobId,
	})

}

// Update or Insert Job and steps within it
func UpSertJobWithStep(ctx *gin.Context) {
	var jobReq struct {
		db.Job
		Steps []map[string]interface{} `json:"steps"`
	}
	if err := ctx.BindJSON(&jobReq); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": "invalid request",
		})
		return
	}
	// save job
	jobReq.Job.Status = "Saved"
	user := user(ctx)
	jobReq.Job.UpdatedBy = user

	if err := db.Conn().Save(&jobReq.Job).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": err})
		return
	}

	if len(jobReq.Steps) == 0 {
		ctx.JSON(http.StatusOK, gin.H{
			"msg": "success",
		})
		return
	}
	// upsert steps of this job
	if jobReq.Type == "Import" {
		var steps []db.ImportStep
		mapstructure.Decode(jobReq.Steps, &steps)
		for i := range steps {
			steps[i].JobId = jobReq.ID
			steps[i].Sequence = uint(i)
			if steps[i].Status == "Draft" {
				steps[i].Status = "Saved"
			}
			steps[i].UpdatedBy = user
		}
		if err := db.Conn().Save(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while saving import steps: %s", err),
			})
			return
		}
	} else if jobReq.Type == "Deploy" {
		var steps []db.DeployStep
		mapstructure.Decode(jobReq.Steps, &steps)
		for i := range jobReq.Steps {
			steps[i].JobId = jobReq.ID
			steps[i].Sequence = uint(i)
			if steps[i].Status == "Draft" {
				steps[i].Status = "Saved"
			}
			steps[i].UpdatedBy = user
		}
		if err := db.Conn().Save(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while saving deploy steps: %s", err),
			})
			return
		}
	} else if jobReq.Type == "Undeploy" {
		// todo
	} else {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("invalid job type %s", jobReq.Type),
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"msg": "success",
	})
}
func ExecuteJob(ctx *gin.Context) {
	jobId, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("invalid job id: %s", err),
		})
		return
	}
	user := user(ctx)

	// query job
	var job db.Job
	if err := db.Conn().First(&job, jobId).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("failed to query job %d", jobId),
		})
		return
	}
	//execute and update steps
	if job.Type == "Import" {
		var steps []db.ImportStep
		if err := db.Conn().Where(&db.ImportStep{JobId: job.ID}).Find(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("failed to query steps of job %d", jobId)})
			return
		}
		for _, step := range steps {
			actionId, ExecErr := executeImport(ctx, step)
			if ExecErr != nil {
				db.Conn().Model(&step).Updates(&db.ImportStep{Status: "Error"})
				db.Conn().Create(&db.ExecutionLog{JobId: job.ID, StepId: step.ID, Sequence: step.Sequence, StepType: job.Type, Log: ExecErr.Error()})
				return
			}
			if err := db.Conn().Model(&step).Updates(db.ImportStep{ActionId: actionId, Status: "Running"}).Error; err != nil {
				ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("err while updating execution status of step %d", step.ID)})
				return
			}
		}
	} else if job.Type == "Deploy" {
		var steps []db.DeployStep
		if dbErr := db.Conn().Where(&db.DeployStep{JobId: job.ID}).Find(&steps).Error; dbErr != nil {
			ctx.JSON(http.StatusInternalServerError, fmt.Sprintf("error while querying steps of job %d: %s", job.ID, dbErr))
			return
		}
		// execute steps
		for _, step := range steps {
			if step.Status == "Finished" || len(step.ArtifactIds) == 0 { // skip Running/Finished
				continue
			}
			// execute Saved/Error steps
			taskIds, taskStatuses, execErr := executeDeploy(ctx, step)
			// update actionIds
			if dbErr := db.Conn().Model(&step).Updates(db.DeployStep{
				TaskIds:      pq.StringArray(taskIds),
				Status:       If(execErr == nil, "Running", "Error"),
				TaskStatuses: pq.StringArray(taskStatuses),
			}).Error; dbErr != nil {
				ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("error while updating execution status of step %d in db: %s", step.ID, dbErr)})
				return
			}
			if execErr != nil { // execution error
				db.Conn().Create(&db.ExecutionLog{JobId: job.ID, StepId: step.ID, Sequence: step.Sequence, StepType: job.Type, Log: execErr.Error()})
				return
			}
		}
	} else if job.Type == "Undeploy" {
		//todo
	} else {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("unexpected job type: %s", job.Type),
		})
	}

	db.Conn().Model(&job).Updates(db.Job{Status: "Running", TriggeredBy: user})

	ctx.JSON(http.StatusAccepted, gin.H{
		"msg": "Job Triggered",
	})

}

// returns job id
func executeImport(ctx context.Context, step db.ImportStep) (uint, error) {
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
func executeDeploy(ctx context.Context, step db.DeployStep) ([]string, []string, error) {
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
			taskIds[0] = "0"
			continue
		}
		taskStatuses[i] = "Running"
		taskIds[i] = actionID
	}
	return taskIds, taskStatuses, err
}

func If(isTrue bool, a, b string) string {
	if isTrue {
		return a
	}
	return b
}

// check execution status of running DEPLOY steps then return summary status of the job
// update steps' status to: Error/Running/Success; return job status: Error/Running/Success/Unknown
func checkDeployStatus(ctx context.Context, steps []db.DeployStep) (string, error) {
	jobStatusSet := make(map[string]int, 0)
	for i, step := range steps { // Running/Error steps should check status
		if step.Status == "Success" || step.Status == "Saved" || len(step.TaskIds) == 0 { // taskids is 0/nil means deploying has not been triggerd yet
			jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1
			continue
		}
		statusSet := make(map[string]bool, 0)
		for j, taskId := range step.TaskIds { // each artifact as a delpoy task id, check status of each task
			// possible statuses of CPI response: Success, Fail, Deploying, Fail_On_License_Error
			if step.TaskStatuses[j] == "Success" { // skip already finished tasks
				continue
			}
			cpiClient, err := cpi.NewClient(ctx, step.Endpoint) // check task status via cpi endpoint
			if err != nil {
				return "", fmt.Errorf("failed to connect to cpi endpoint: %s: %s", step.Endpoint, err)
			}
			status, err := cpiClient.CheckDeployStatus(taskId)
			if err != nil {
				return "", fmt.Errorf("error while checking status of step %d, taskId: %s: %s", i, taskId, err)
			}
			step.TaskStatuses[j] = status
			statusSet[status] = true
		}
		// map step status to Error/Running/Success
		if statusSet["Fail"] || statusSet["Fail_On_License_Error"] {
			step.Status = "Error"
		} else if statusSet["Deploying"] {
			step.Status = "Running"
		} else {
			step.Status = "Success"
		}

		jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1 // recode status count
		if err := db.Conn().Model(&step).Updates(step).Error; err != nil {
			return "", fmt.Errorf("error while updating status of step %d: %s", i, err)
		}
	}
	// summarize job status
	jobStatus := jobStatus(jobStatusSet)
	return jobStatus, nil
}

// update steps' status to: Error/Running/Success, or warning, initial, unkown.
// return job status: Error/Running/Success/Unknown
func checkImportStatus(ctx context.Context, steps []db.ImportStep) (string, error) {
	cpiClient, err := tms.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to connect to tms: %s", err)
	}
	jobStatusSet := make(map[string]int, 0)
	for i, step := range steps {
		if step.Status == "Success" || step.Status == "Saved" || step.ActionId == 0 {
			jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1
			continue
		}
		// possible statuses of tms action: succeeded, warning, error, fatal, running, initial, unknown
		status, err := cpiClient.GetActionResult(step.ActionId)
		if err != nil {
			return "", fmt.Errorf("error while getting status of action id %d of step %d", step.ActionId, i)
		}
		// update status, map to Finished/Error/Running
		if status == "succeeded" {
			status = "Success"
		} else if status == "error" || status == "fatal" {
			status = "Error"
		} else if status == "running" {
			status = "Running"
		}
		// or use TMS status directly: warning, initial, unknown
		step.Status = status
		jobStatusSet[step.Status] = jobStatusSet[step.Status] + 1
		if err := db.Conn().Model(&step).Updates(step).Error; err != nil {
			return "", fmt.Errorf("error while updating status of step %d (actionId: %d): %s", i, step.ActionId, err)
		}
	}
	// update job status
	jobStatus := jobStatus(jobStatusSet)
	return jobStatus, nil
}

func user(ctx *gin.Context) string {
	email, _ := ctx.Get("user")
	return email.(string)
}

// Return job status based on steps' statusSet. Possible statuses: Error/Running/Success/Unknown or Saved
func jobStatus(statusSet map[string]int) string {
	if statusSet["Error"] > 0 {
		return "Error"
	} else if statusSet["Running"] > 0 {
		return "Running"
	} else if statusSet["Saved"] > 0 {
		return "Saved"
	} else if statusSet["Success"] > 0 {
		return "Success"
	}
	return "Unknown"
}
