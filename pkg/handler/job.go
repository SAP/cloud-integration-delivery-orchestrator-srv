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
	var config db.CreateJobParams
	err := ctx.BindJSON(&config)
	if err != nil {
		logger.Errorf("invalid request, error message is %s", err)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	} else {
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
		configResp, error2 := query.CreateJob(context, config)
		if error2 != nil {
			logger.Errorf("Error when storing job database, error message is %s", error2)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when retrieve config from database",
				"code":   http.StatusServiceUnavailable,
			})
			return
		} else {
			ctx.JSON(http.StatusOK, gin.H{
				"status": "success",
				"code":   200,
				"result": configResp,
			})
		}
	}
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

	configs, errorQuery := query.GetJobs(context)
	if errorQuery != nil {
		logger.Errorf("Error when retrieve jobs from database, error message is %s", errorQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": configs,
	})
}
func GetJobyID(ctx *gin.Context) {

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
	logger.Info(id)
	if id == "" {
		logger.Error("invalid request, please supply the id in the url")
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	}

	idnumber, _ := strconv.Atoi(id)
	logger.Infof("getting config with id %d", idnumber)
	config, errorDBQuery := query.GetJobByID(context, idnumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when retrieve job from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": config,
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

	config, errorDBQuery := query.DeleteJobByID(context, idnumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when retrieve config from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"msg":    "deleted",
		"code":   http.StatusOK,
		"id":     config.ID,
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
	var config db.UpdateJobByIDParams

	err := ctx.BindJSON(&config)
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
	config.ID = idNumber
	logger.Infof("config %#v", config)

	configresp, errorDBQuery := query.UpdateJobByID(context, config)
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
		"result": configresp,
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
	jobConfig, errorDBQuery := query.GetJobByID(context, int32(idNumber))
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
		templType  := stepConfig.TemplType
		templID := stepConfig.TemplID
		switch templType {
			case "import":{
				tmsTmpl, errorTMS:= query.GetTMStmplByID(context, stepConfig.TemplID)
				if errorTMS !=nil {
					logger.Errorf("Error when getting tms import config from database, error message is %s", errorTMS)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting tms import config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				logger.Infof("Starting to execute import task id %d", templID)
				tmpConfig, errorTmsConfig := query.GetConfigByID(context, tmsTmpl.CpiConfigID)
				if errorTmsConfig !=nil {
					logger.Errorf("Error when getting tms config from database, error message is %s", errorConfig)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting tms config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				tmsClient, errTmsClient:=tms.NewTMSClient(context,tmpConfig.AuthClientID,tmpConfig.AuthClientSecret,tmpConfig.AuthUrl, tmpConfig.ApiUrl)
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
				if errorImport !=nil {
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
					status = tmsClient.GetActionResult(importActionID)
					time.Sleep(time.Second * 15)
				}

				if status != "SUCCESS" {
					actionResp, _:=tmsClient.GetActionResultLog(importActionID)
					logger.Errorf("Error when  importting transport requests, error message is %s", errTmsClient)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when importing job",
						"code":   http.StatusServiceUnavailable,
						"result": actionResp,
					})
					continue
				}
				logger.Info("Transport requests %v is/are import successfully for job %s", tmsTmpl.TmsTrIds,id)
				ctx.JSON(http.StatusOK, gin.H{
					"status": "success",
					"msg":    "imported all transport request successfully",
					"code":   http.StatusOK,
					"result": actionResp,
				})

			}
			case "deploy": {
				cpiTmpl, errorTMS:= query.GetCPItmplByID(context, stepConfig.TemplID)
				if errorCPI !=nil {
					logger.Errorf("Error when getting tms import config from database, error message is %s", errorCPI)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting cpi import config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				logger.Infof("Starting to execute deployment task id %d", templID)

				cpiConfig, errorCpiConfig := query.GetConfigByID(context, cpiTmpl.CpiConfigID)
				if errorCpiConfig !=nil {
					logger.Errorf("Error when getting cpi config from database, error message is %s", errorConfig)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when getting cpi config from database",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}

				cpiClient, errorCpiClient := cpi.NewCPIClient(context, cpiConfig.AuthClientID, cpiConfig.AuthClientSecret, cpiConfig.AuthUrl , cpiConfig.ApiUrl)
				if errorCpiClient != nil {
					logger.Errorf("Error when authenticating to cpi tenant, error message is %s", errTmsClient)
					ctx.JSON(http.StatusServiceUnavailable, gin.H{
						"status": "failed",
						"msg":    "Error when authenticating to cpi tenant",
						"code":   http.StatusServiceUnavailable,
					})
					continue
				}
				cpiClient.DeployIflow(packageID string, iflowID string, iflowVersion string)



			}
			case "undeploy":{

			}
		}


}
