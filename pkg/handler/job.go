package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	// "time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
	// "github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	// "github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
)

type Step interface {
	GetSeq() int
	Execute(ctx *gin.Context, query *db.Queries) error
}
type ImportStepResp struct {
	db.ImportStep
	Type string `json:"type"`
}
type DeployStepResp struct {
	db.DeployStep
	Type string `json:"type"`
}

func (s *ImportStepResp) GetSeq() int {
	return s.Sequence
}

func (s *DeployStepResp) GetSeq() int {
	return s.Sequence
}

func (s *ImportStepResp) Execute(ctx *gin.Context, query *db.Queries) error {
	logger.Infof("Starting to execute import task id %d", s.ID)
	apiEndpoint, error := query.GetApiEndpointById(ctx, s.EndpointID)
	if error != nil {
		return error
	}
	tmsClient, error := tms.NewTMSClient(ctx, apiEndpoint.ClientId, apiEndpoint.ClientSecret, apiEndpoint.AuthUrl, apiEndpoint.ApiUrl)
	if error != nil {
		return error
	}

	importActionID, error := tmsClient.ImportTransportRequest(s.TransportNodeID, s.TransportRequests)
	if error != nil {
		return fmt.Errorf("error while importing Transport Requests on node (%s): %v: %s", s.TransportNodeName, s.TransportRequests, error)
	}
	status := "DEPLOYING"
	for status == "DEPLOYING" {
		status, _ = tmsClient.GetActionResult(importActionID)
		time.Sleep(time.Second * 15)
	}
	actionResp, _ := tmsClient.GetActionResultLog(importActionID)
	logger.Info(actionResp)
	if status != "SUCCESS" {
		return fmt.Errorf("error when importting transport requests: %s", error)
	}
	logger.Info("Transport requests %v is/are import successfully for job %s", s.TransportRequests, s.JobID)
	return nil

}

func (s *DeployStepResp) Execute(ctx *gin.Context, query *db.Queries) error {
	logger.Infof("Starting to execute deployment task %d", s.JobID)

	apiEndpoint, errorCpiConfig := query.GetApiEndpointById(ctx, s.EndpointID)
	if errorCpiConfig != nil {
		return fmt.Errorf("error when getting cpi config from database, error message is %s", errorCpiConfig)
	}

	cpiClient, errorCpiClient := cpi.NewCPIClient(ctx, apiEndpoint.ClientId, apiEndpoint.ClientSecret, apiEndpoint.AuthUrl, apiEndpoint.ApiUrl)
	if errorCpiClient != nil {
		return fmt.Errorf("error when authenticating to cpi tenant, error message is %s", errorCpiClient)
	}
	for _, iflow := range s.ArtifactIds {
		cpiClient.DeployIflow(string(iflow), "active")
	}
	return nil
}

type JobResp struct {
	db.Job
	Steps []Step `json:"steps"`
}

// Get job detail with step list
func GetJobByID(ctx *gin.Context) {
	jobParam := ctx.Param("id")
	jobId, err := strconv.Atoi(jobParam)
	if err != nil {
		return
	}

	job, steps, err := getJobwithSteps(ctx, jobId)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"result": err,
		})
		return
	}

	jobResp := &JobResp{
		*job,
		steps,
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": jobResp,
	})
}

func getJobwithSteps(ctx *gin.Context, jobId int) (*db.Job, []Step, error) {
	conn, err := pgx.Connect(ctx, DBSource)
	if err != nil {
		return nil, nil, err
	}
	query := db.New(conn)
	// get import steps
	importSteps, err := query.SelectImportStepsByJobId(ctx, jobId)
	if err != nil {
		return nil, nil, err
	}
	// get deploy steps
	deploySteps, err := query.SelectDeployStepsByJobId(ctx, jobId)
	if err != nil {
		return nil, nil, err
	}

	steps := make([]Step, 0)
	for _, p := range importSteps {
		steps = append(steps, &ImportStepResp{p, "Import"})
	}
	for _, p := range deploySteps {
		steps = append(steps, &DeployStepResp{p, "Deploy"})
	}
	// sort by sequence
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].GetSeq() < steps[j].GetSeq()
	})

	// get job
	job, err := query.GetJobByID(ctx, jobId)
	if err != nil {
		return nil, nil, err
	}
	return &job, steps, nil
}

func CreateJob(ctx *gin.Context) {
	context := ctx.Request.Context()
	var createJobParams db.CreateJobParams
	err := ctx.BindJSON(&createJobParams)
	if err != nil {
		logger.Errorf("invalid request, error message is %s", err)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	}
	dbConn, errDBconn := pgx.Connect(context, DBSource)
	if errDBconn != nil {
		logger.Errorf("error when connecting to the database, please contact your system administrator, error message is %s", errDBconn)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "error when connecting to the database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	query := db.New(dbConn)
	createJobResp, error2 := query.CreateJob(context, createJobParams)
	if error2 != nil {
		logger.Errorf("Error when creating job, error message is %s", error2)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when creating job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": createJobResp,
	})

}

func GetJobs(ctx *gin.Context) {
	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, DBSource)
	if errDBconn != nil {
		logger.Errorf("error when connecting to the database, please contact your system administrator, error message is %s", errDBconn)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "error when connecting to the database, please contact your system administrator",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	query := db.New(dbConn)

	jobs, errorQuery := query.GetJobs(context)
	if errorQuery != nil {
		logger.Errorf("Error when retrieve jobs from database, error message is %s", errorQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve jobs from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": jobs,
	})
}
func GetJobyID(ctx *gin.Context) {

	context := ctx.Request.Context()
	id := ctx.Param("id")
	if id == "" {
		logger.Error("invalid request, please supply the id in the url")
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	}

	dbConn, errDBconn := pgx.Connect(context, DBSource)
	if errDBconn != nil {
		logger.Errorf("error when connecting to the database, please contact your system administrator, error message is %s", errDBconn)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "error when connecting to the database, please contact your system administrator",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	query := db.New(dbConn)

	idnumber, _ := strconv.Atoi(id)
	logger.Infof("getting job with id %d", idnumber)
	job, errorDBQuery := query.GetJobByID(context, idnumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when retrieve job from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve job from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": job,
	})
}
func DeleteJob(ctx *gin.Context) {

	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, DBSource)
	if errDBconn != nil {
		logger.Errorf("error when connecting to the database, please contact your system administrator, error message is %s", errDBconn)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "error when connecting to the database, please contact your system administrator",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	query := db.New(dbConn)
	id := ctx.Param("id")
	if id == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "no id available, please specify the id of config",
			"code":   http.StatusBadRequest,
		})
		return
	}
	idnumber, _ := strconv.Atoi(id)

	job, errorDBQuery := query.DeleteJobByID(context, idnumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when deleting job, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when deleting job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"msg":    "deleted",
		"code":   http.StatusOK,
		"id":     job.ID,
	})

}

type StepReq struct {
	Id                int    `json:"id"`
	Status            string `json:"status"`
	Type              string `json:"type"`
	EndpointId        int    `json:"endpoint_id"`
	TransportNodeId   int    `json:"transport_node_id"`
	TransportNodeName string `json:"transport_node_name"`
	TransportRequests []int  `json:"transport_requests"`
	PackageId         int    `json:"package_id"`
	ArtifactIds       []int  `json:"artifact_ids"`
}
type JobReq struct {
	db.Job
	Steps []StepReq `json:"steps"`
}

// Update Job and steps within it
func UpdateJobAndStep(ctx *gin.Context) {
	var jobReq JobReq

	err := ctx.BindJSON(&jobReq)
	if err != nil {
		logger.Errorf("invalid request, error message is %s", err)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	}

	dbConn, errDBconn := pgx.Connect(ctx, DBSource)
	if errDBconn != nil {
		logger.Errorf("error when connecting to the database, please contact your system administrator, error message is %s", errDBconn)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "error when connecting to the database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	query := db.New(dbConn)
	// Update Job itself
	updateJobParams := &db.UpdateJobByIDParams{
		ID:          jobReq.ID,
		Name:        jobReq.Description,
		Description: jobReq.Name,
		Status:      "Submitted",
	}

	updateJobByIDResp, errorDBQuery := query.UpdateJobByID(ctx, *updateJobParams)
	if errorDBQuery != nil {
		logger.Errorf("Error when updating job from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when updating job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	// Update steps within the step
	for i, step := range jobReq.Steps {
		if step.Type == "Import" {
			if step.Id == -1 { //Do Insert
				_, err := query.InsertImportStep(ctx, db.InsertImportStepParams{
					JobID:             jobReq.ID,
					Status:            "Submitted",
					Sequence:          i,
					EndpointID:        step.EndpointId,
					TransportNodeID:   step.TransportNodeId,
					TransportNodeName: step.TransportNodeName,
					TransportRequests: step.TransportRequests,
				})
				if err != nil {
					ctx.JSON(http.StatusInternalServerError, gin.H{
						"msg": fmt.Sprintf("Internal Server Error: %s", err),
					})
					return
				}
				continue
			}
			//Do Update
			_, err := query.UpdateImportStep(ctx, db.UpdateImportStepParams{
				ID:                step.Id,
				Sequence:          i,
				Status:            "Submitted",
				EndpointID:        step.EndpointId,
				TransportNodeID:   step.TransportNodeId,
				TransportNodeName: step.TransportNodeName,
				TransportRequests: step.TransportRequests,
			})
			if err != nil {
				ctx.JSON(http.StatusInternalServerError, gin.H{
					"msg": fmt.Sprintf("Internal Server Error: %s", err),
				})
				return
			}
		} else if step.Type == "Deploy" {

		}

	}

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": updateJobByIDResp,
	})

}
func ExecuteJob(ctx *gin.Context) {
	context := ctx.Request.Context()
	id := ctx.Param("id")
	if id == "" {
		logger.Error("invalid request, please supply the id in the url")
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	}

	logger.Infof("get job config for %s", id)
	dbConn, errDBconn := pgx.Connect(context, DBSource)
	if errDBconn != nil {
		logger.Errorf("error when connecting to the database, please contact your system administrator, error message is %s", errDBconn)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "error when connecting to the database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	query := db.New(dbConn)
	idNumber, _ := strconv.Atoi(id)
	_, errorDBQuery := query.GetJobByID(context, idNumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when getting job from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when executing job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	job, steps, err := getJobwithSteps(ctx, idNumber)
	if err != nil {
		return
	}

	logger.Infof("job %d has steps %v", id, steps)
	for i, step := range steps {
		logger.Infof("Processing step %d of job %d ", i, job.ID)
		step.Execute(ctx, query)
	}
}
