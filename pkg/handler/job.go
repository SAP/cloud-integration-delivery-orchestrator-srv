package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	// "time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
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
		if err = db.Conn().Where(db.ImportStep{JobId: job.ID}).Order("sequence").Find(&importSteps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError,
				gin.H{"msg": fmt.Sprintf("error while querying Import Steps of job %d: %s", job.ID, err)},
			)
			return
		}
		if jobStatus, err = CheckImportStatus(ctx, importSteps); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while updating status of steps: %s", err),
			})
			return
		}
		steps = importSteps
	} else if job.Type == "Deploy" {
		var deploySteps []db.DeployStep
		var err error
		if err = db.Conn().Where(db.DeployStep{JobId: job.ID}).Order("sequence").Find(&deploySteps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError,
				gin.H{"msg": fmt.Sprintf("error while querying Deploy Steps of job %d: %s", job.ID, err)},
			)
			return
		}
		if jobStatus, err = CheckDeployStatus(ctx, deploySteps); err != nil {
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
	job.ID = 0
	job.Status = JOB_STATUS_SAVED
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

func CopyJob(ctx *gin.Context) {
	fromJobId, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("invalid job id %d: %s", fromJobId, err),
		})
		return
	}

	var job db.Job
	if err := db.Conn().First(&job, fromJobId).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("error while querying job %d: %s", fromJobId, err),
		})
		return
	}

	job.ID = 0
	job.Name = "Copy of - " + job.Name
	job.Status = JOB_STATUS_SAVED
	job.CreatedBy = user(ctx)
	job.UpdatedBy = ""
	job.Description = "Copied Desctiption - " + job.Description
	// create a new job with the same steps. will write a new Job id
	if err := db.Conn().Create(&job).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("error while copying job: %s", err),
		})
		return
	}
	if job.Type == "Import" {
		var steps []db.ImportStep
		// query steps of the job
		if err := db.Conn().Where(&db.ImportStep{JobId: uint(fromJobId)}).Find(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while querying import steps of job %d: %s", fromJobId, err),
			})
			return
		}
		for i := range steps {
			steps[i].ID = 0
			steps[i].JobId = job.ID
			steps[i].Status = STEP_STATUS_SAVED
			steps[i].UpdatedBy = user(ctx)
			steps[i].Sequence = uint(i)
			steps[i].TransportRequests = pq.Int32Array{}
			steps[i].ActionId = 0
		}
		if err := db.Conn().Create(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while copying import steps: %s", err),
			})
			return
		}
	} else if job.Type == "Deploy" {
		var steps []db.DeployStep
		if err := db.Conn().Where(&db.DeployStep{JobId: uint(fromJobId)}).Find(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while querying deploy steps of job %d: %s", fromJobId, err),
			})
			return
		}
		for i := range steps {
			steps[i].ID = 0
			steps[i].JobId = job.ID
			steps[i].Status = STEP_STATUS_SAVED
			steps[i].UpdatedBy = user(ctx)
			steps[i].Sequence = uint(i)
			steps[i].ArtifactIds = pq.StringArray{}
			steps[i].ArtifactTypes = pq.StringArray{}
			steps[i].ArtifactVersions = pq.StringArray{}
			steps[i].TaskIds = pq.StringArray{}
			steps[i].TaskStatuses = pq.StringArray{}
		}
		if err := db.Conn().Create(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while copying deploy steps: %s", err),
			})
			return
		}

	} else if job.Type == "Undeploy" {

	} else {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("invalid job type: %s", job.Type),
		})
		return
	}

	ctx.Params = []gin.Param{
		{Key: "id", Value: strconv.Itoa(int(job.ID))},
	}
	GetJobAndStepsByID(ctx)
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
	// error or running steps can't be deleted
	var job db.Job
	if err := db.Conn().First(&job, jobId).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("error while querying job %d: %s", jobId, err),
		})
		return
	}
	if job.Status == JOB_STATUS_RUNNING || job.Status == JOB_STATUS_ERROR {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": "Job is running or in error status, can't be deleted",
		})
		return
	}

	if err := db.Conn().Delete(&db.Job{}, jobId).Error; err != nil {
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"result": jobId,
	})

}

type DeployJob struct {
	db.Job
	Steps []db.DeployStep
}

type ImportJob struct {
	db.Job
	Steps []db.ImportStep
}

// Update or Insert Job and steps within it
func UpSertJobWithStep(ctx *gin.Context) {
	request, err := ctx.GetRawData()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "invalid request"})
		return
	}
	// parse job
	var job db.Job
	if err := json.Unmarshal(request, &job); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "faied to unmarshal request: " + err.Error()})
		return
	}
	// save job
	job.Status = JOB_STATUS_SAVED
	user := user(ctx)
	job.UpdatedBy = user

	if err := db.Conn().Save(&job).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": err})
		return
	}
	// upsert steps of this job
	if job.Type == "Import" {
		var importJob ImportJob
		if err := json.Unmarshal(request, &importJob); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "failed to unmarshal request: " + err.Error()})
			return
		}
		steps := importJob.Steps
		for i := range steps {
			// running, success, error steps can't be updated
			if steps[i].Status == STEP_STATUS_RUNNING || steps[i].Status == STEP_STATUS_SUCCESS || steps[i].Status == STEP_STATUS_ERROR {
				continue
			}
			steps[i].JobId = job.ID
			steps[i].Sequence = uint(i)
			steps[i].Status = STEP_STATUS_SAVED
			steps[i].UpdatedBy = user
		}
		if err := db.Conn().Save(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while saving import steps: %s", err),
			})
			return
		}
	} else if job.Type == "Deploy" {
		var deployJob DeployJob
		if err := json.Unmarshal(request, &deployJob); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "failed to unmarshal request: " + err.Error()})
			return
		}
		steps := deployJob.Steps
		for i := range steps {
			// running, success, error steps can't be updated
			if steps[i].Status == STEP_STATUS_RUNNING || steps[i].Status == STEP_STATUS_SUCCESS || steps[i].Status == STEP_STATUS_ERROR {
				continue
			}
			steps[i].JobId = job.ID
			steps[i].Sequence = uint(i)
			steps[i].Status = STEP_STATUS_SAVED
			steps[i].UpdatedBy = user
		}
		if err := db.Conn().Save(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while saving deploy steps: %s", err),
			})
			return
		}
	} else if job.Type == "Undeploy" {
		// todo
	} else {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("invalid job type %s", job.Type),
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
			if (step.Status == STEP_STATUS_SUCCESS || step.Status == STEP_STATUS_RUNNING) && step.ActionId != 0 { // skip Running/Success. 0 means not triggered successfully
				continue
			}
			actionId, ExecErr := ExecuteImport(ctx, step)
			if ExecErr != nil {
				db.Conn().Model(&step).Updates(&db.ImportStep{Status: STEP_STATUS_ERROR})
				db.Conn().Create(&db.ExecutionLog{JobId: job.ID, StepId: step.ID, Sequence: step.Sequence, StepType: job.Type, Log: ExecErr.Error()})
				return
			}
			// import job triggerred, update step status and trigger info, record actionId
			if err := db.Conn().Model(&step).Updates(db.ImportStep{
				ActionId: actionId, Status: STEP_STATUS_RUNNING,
				TriggeredBy: user,
				TriggeredAt: time.Now(),
			}).Error; err != nil {
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
			if step.Status == STEP_STATUS_SUCCESS || step.Status == STEP_STATUS_RUNNING || len(step.ArtifactIds) == 0 { // skip Running/Success
				continue
			}
			// execute Saved/Error steps
			var taskIds, taskStatuses []string
			var execErr error
			if step.Type == "Deploy" {
				taskIds, taskStatuses, execErr = ExecuteDeploy(ctx, step)
			} else if step.Type == "Undeploy" {
				taskIds, taskStatuses, execErr = ExecuteUndeploy(ctx, step)
			} else {
				ctx.JSON(http.StatusBadRequest, gin.H{"msg": fmt.Sprintf("unexpected step type: %s", step.Type)})
				return
			}
			// update taskIds and status of steps
			if dbErr := db.Conn().Model(&step).Updates(db.DeployStep{
				TaskIds:      pq.StringArray(taskIds),
				Status:       If(execErr == nil, STEP_STATUS_RUNNING, STEP_STATUS_ERROR),
				TaskStatuses: pq.StringArray(taskStatuses),
				TriggeredBy:  user,
				TriggeredAt:  time.Now(),
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

	db.Conn().Model(&job).Updates(db.Job{Status: JOB_STATUS_RUNNING, TriggeredBy: user})

	ctx.JSON(http.StatusAccepted, gin.H{
		"msg": "Job Triggered",
	})

}

func If(isTrue bool, a, b string) string {
	if isTrue {
		return a
	}
	return b
}

func user(ctx *gin.Context) string {
	email, _ := ctx.Get("user")
	return email.(string)
}

// Return job status based on steps' statusSet. Possible statuses: Error/Running/Success/Unknown or Saved
func mapToJobStatus(statusSet map[string]int) string {
	if statusSet[STEP_STATUS_ERROR] > 0 {
		return JOB_STATUS_ERROR
	} else if statusSet[STEP_STATUS_RUNNING] > 0 {
		return JOB_STATUS_RUNNING
	} else if statusSet[STEP_STATUS_SAVED] > 0 {
		return JOB_STATUS_SAVED
	} else if statusSet[STEP_STATUS_SUCCESS] > 0 {
		return JOB_STATUS_SUCCESS
	}
	return JOB_STATUS_UNKNOWN
}

const (
	STEP_STATUS_ERROR   = "Error"
	STEP_STATUS_RUNNING = "Running"
	STEP_STATUS_SUCCESS = "Success"
	STEP_STATUS_SAVED   = "Saved"
	STEP_STATUS_DRAFT   = "Draft"

	JOB_STATUS_ERROR   = "Error"
	JOB_STATUS_RUNNING = "Running"
	JOB_STATUS_SUCCESS = "Success"
	JOB_STATUS_UNKNOWN = "Unknown"
	JOB_STATUS_SAVED   = "Saved"

	DEPLOY_STATUS_SUCCESS               = "SUCCESS"
	DEPLOY_STATUS_FAIL                  = "FAIL"
	DEPLOY_STATUS_DEPLOYING             = "DEPLOYING"
	DEPLOY_STATUS_FAIL_ON_LICENSE_ERROR = "FAIL_ON_LICENSE_ERROR"

	UNDEPLOY_STATUS_UNDEPLOYING = "UNDEPLOYING"
	UNDEPLOY_STATUS_SUCCESS     = DEPLOY_STATUS_SUCCESS
	UNDEPLOY_STATUS_FAIL        = DEPLOY_STATUS_FAIL
)
