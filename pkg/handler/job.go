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

	if job.Type == "Import" {
		var importSteps []db.ImportStep
		if err := db.Conn().Where(db.ImportStep{JobId: job.ID}).Find(&importSteps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError,
				gin.H{"msg": fmt.Sprintf("error while querying Import Steps of job %d: %s", job.ID, err)},
			)
			return
		}
		steps = importSteps
	} else if job.Type == "Deploy" {
		var deployStep []db.DeployStep
		if err := db.Conn().Where(db.DeployStep{JobId: job.ID}).Find(&deployStep).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError,
				gin.H{"msg": fmt.Sprintf("error while querying Deploy Steps of job %d: %s", job.ID, err)},
			)
			return
		}
		steps = deployStep
	} else if job.Type == "Undepoloy" {
		// todo
	} else {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("invalid job type: %s", job.Type),
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"result": struct {
			db.Job
			Steps any `json:"Steps"`
		}{
			job,
			steps,
		},
	})
}

func CreateJob(ctx *gin.Context) {
	var job db.Job = db.Job{}
	if err := ctx.BindJSON(&job); err != nil {
		logger.Errorf("invalid request, error message is %s", err)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": "invalid request param",
		})
		return
	}
	job.Status = "Submitted"
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
			steps[i].Status = "Saved"
		}
		if err := db.Conn().Save(&steps).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{
				"msg": fmt.Sprintf("error while saving steps: %s", err),
			})
			return
		}

	} else if jobReq.Type == "Deploy" {
		var steps []db.DeployStep
		mapstructure.Decode(jobReq.Steps, &steps)
		for i := range jobReq.Steps {
			steps[i].JobId = jobReq.ID
			steps[i].Sequence = uint(i)
			steps[i].Status = "Saved"
		}
		db.Conn().Save(&steps)
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
	// query job
	var job db.Job
	if err := db.Conn().First(&job, jobId).Error; err != nil {
		return
	}
	//execute and update steps
	if job.Type == "Import" {
		var steps []db.ImportStep
		if err := db.Conn().Where(&db.ImportStep{JobId: job.ID}).Find(&steps).Error; err != nil {
			return
		}
		for _, step := range steps {
			actionId, err := executeImport(ctx, step)
			if err != nil {
				return
			}
			if err := db.Conn().Model(&step).Updates(db.ImportStep{ActionId: actionId, Status: "Running"}).Error; err != nil {
				return
			}
		}
	} else if job.Type == "Deploy" {
		var steps []db.DeployStep
		if err := db.Conn().Where(&db.DeployStep{JobId: job.ID}).Find(&steps).Error; err != nil {
			return
		}
		// execute steps
		for _, step := range steps {
			taskIds, err := executeDeploy(ctx, step)
			if err != nil {
				db.Conn().Model(&db.Job{}).Where("id=?", job.ID).Update("status", "Error")
				return
			}
			// update actionIds
			if err := db.Conn().Model(&step).Updates(db.DeployStep{TaskIds: pq.StringArray(taskIds), Status: "Running"}).Error; err != nil {
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

}

// returns job id
func executeImport(ctx context.Context, step db.ImportStep) (uint, error) {
	client, err := tms.NewClient(ctx)
	if err != nil {
		return 0, err
	}
	actionId, err := client.ImportTransportRequest(step.TransportNodeId, step.TransportRequests)
	if err != nil {
		return 0, err
	}
	return actionId, nil
}

// returns taskIds or err
func executeDeploy(ctx context.Context, step db.DeployStep) ([]string, error) {
	client, err := cpi.NewClient(ctx, step.Endpoint)
	if err != nil {
		return nil, err
	}
	taskIds := make([]string, 0)
	for i, artifactId := range step.ArtifactIds {
		var actionID string
		// currently support two types of artifacts
		if step.ArtifactTypes[i] == "Integration Flow" {
			actionID, err = client.DeployIflow(artifactId, "active")
			if err != nil {
				return nil, err
			}
		} else if step.ArtifactTypes[i] == "Script Collection" {
			actionID, err = client.DeployScriptCollection(artifactId, "active")
			if err != nil {
				return nil, err
			}
		}
		taskIds = append(taskIds, actionID)
	}
	return taskIds, nil
}
