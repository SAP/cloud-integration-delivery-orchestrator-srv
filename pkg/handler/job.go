package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
)

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
	dbConn, errDBconn := pgx.Connect(context, dbSource)
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
	dbConn, errDBconn := pgx.Connect(context, dbSource)
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

	dbConn, errDBconn := pgx.Connect(context, dbSource)
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
	dbConn, errDBconn := pgx.Connect(context, dbSource)
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
func UpdateJob(ctx *gin.Context) {

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
	var updateJobByIDParams db.UpdateJobByIDParams

	err := ctx.BindJSON(&updateJobByIDParams)
	if err != nil {
		logger.Errorf("invalid request, error message is %s", err)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	}
	dbConn, errDBconn := pgx.Connect(context, dbSource)
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
	updateJobByIDParams.ID = idNumber
	logger.Infof("config %#v", updateJobByIDParams)

	updateJobByIDResp, errorDBQuery := query.UpdateJobByID(context, updateJobByIDParams)
	if errorDBQuery != nil {
		logger.Errorf("Error when updating job from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when updating job",
			"code":   http.StatusServiceUnavailable,
		})
		return
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
	dbConn, errDBconn := pgx.Connect(context, dbSource)
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
	jobConfig, errorDBQuery := query.GetJobByID(context, idNumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when getting job from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when executing job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	logger.Infof("job %d has steps %v", id, jobConfig.Steps)
	for _, stepID := range jobConfig.Steps {
		logger.Infof("Processing step %d of job %d ", stepID, idNumber)

		stepConfig, errerrorDBQuery2 := query.GetStepByID(context, stepID)
		if errerrorDBQuery2 != nil {
			logger.Errorf("Error when getting step config from database, error message is %s", errerrorDBQuery2)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when executing job",
				"code":   http.StatusServiceUnavailable,
			})
			continue
		}
		templType := stepConfig.TemplType
		templID := stepConfig.TemplID
		switch templType {
		case "import":
			{
				tmsTmpl, errorTMS := query.GetTMStmplByID(context, stepConfig.TemplID)
				if errorTMS != nil {
					logger.Errorf("Error when getting tms import config from database, error message is %s", errorTMS)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting tms import config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				logger.Infof("Starting to execute import task id %d", templID)
				tmpConfig, errorTmsConfig := query.GetApiEndpointById(context, tmsTmpl.TmsConfigID)
				if errorTmsConfig != nil {
					logger.Errorf("Error when getting tms config from database, error message is %s", errorTmsConfig)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting tms config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				tmsClient, errTmsClient := tms.NewTMSClient(context, tmpConfig.ClientId, tmpConfig.ClientSecret, tmpConfig.AuthUrl, tmpConfig.ApiUrl)
				if errTmsClient != nil {
					logger.Errorf("Error when authenticating to tms , error message is %s", errTmsClient)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when authenticating to tms",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}

				importActionID, errorImport := tmsClient.ImportTransportRequest(tmsTmpl.TmsNodeID, tmsTmpl.TmsTrIds)
				if errorImport != nil {
					logger.Errorf("Error when initializing import transport requests, error message is %s", errTmsClient)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when initializing import transport requests",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				status := "DEPLOYING"
				for status == "DEPLOYING" {
					status, _ = tmsClient.GetActionResult(importActionID)
					time.Sleep(time.Second * 15)
				}
				actionResp, _ := tmsClient.GetActionResultLog(importActionID)
				if status != "SUCCESS" {
					logger.Errorf("Error when  importting transport requests, error message is %s", errTmsClient)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when importing job",
						"code":   http.StatusServiceUnavailable,
						"result": actionResp,
					})
					continue
				}
				logger.Info("Transport requests %v is/are import successfully for job %s", tmsTmpl.TmsTrIds, id)
				ctx.JSON(http.StatusOK, gin.H{
					"status": "success",
					"msg":    "imported all transport request successfully",
					"code":   http.StatusOK,
					"result": actionResp,
				})

			}
		case "deploy":
			{
				cpiTmpl, errorTMS := query.GetCPItmplByID(context, stepConfig.TemplID)
				if errorTMS != nil {
					logger.Errorf("Error when getting tms import config from database, error message is %s", errorTMS)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting cpi import config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				logger.Infof("Starting to execute deployment task id %d", templID)

				cpiConfig, errorCpiConfig := query.GetApiEndpointById(context, cpiTmpl.CpiConfigID)
				if errorCpiConfig != nil {
					logger.Errorf("Error when getting cpi config from database, error message is %s", errorCpiConfig)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting cpi config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}

				cpiClient, errorCpiClient := cpi.NewCPIClient(context, cpiConfig.ClientId, cpiConfig.ClientSecret, cpiConfig.AuthUrl, cpiConfig.ApiUrl)
				if errorCpiClient != nil {
					logger.Errorf("Error when authenticating to cpi tenant, error message is %s", errorCpiClient)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when authenticating to cpi tenant",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				for _, iflow := range cpiTmpl.CpiIflowIds {
					cpiClient.DeployIflow(iflow, "active")
				}

			}
		case "undeploy":
			{

			}
		}

	}
}
