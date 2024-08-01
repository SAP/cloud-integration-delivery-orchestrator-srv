package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
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
			logger.Errorf("Error when getting step config from database, error message is %s", errorDBQuery)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when executing job",
				"code":   http.StatusServiceUnavailable,
			})
			return
		}
		if stepConfig.TemplType == "import"{
			query.GetCPItmplByID(context, stepConfig.TemplID)
		}
		if stepConfig.TemplType == "deploy"{


		}
		if stepConfig.TemplType == "undeploy"{
	}

}
