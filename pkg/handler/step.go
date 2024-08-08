package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

func CreateStep(ctx *gin.Context) {
	context := ctx.Request.Context()

	var createStepPara db.CreateStepParams
	err := ctx.BindJSON(&createStepPara)
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
	logger.Infof("creating step")
	createStepResp, errorCreateStep := query.CreateStep(context, createStepPara)
	if errorCreateStep != nil {
		logger.Errorf("Error when retrieve config from database, error message is %s", errorCreateStep)
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
		"result": createStepResp,
	})
}

func GetSteps(ctx *gin.Context) {
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

	steps, errorQuery := query.GetSteps(context)
	if errorQuery != nil {
		logger.Errorf("Error when retrieve steps from database, error message is %s", errorQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve steps from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   http.StatusOK,
		"result": steps,
	})
}

func GetStepByID(ctx *gin.Context) {
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
	logger.Infof("getting step with id %d", idnumber)
	step, errorDBQuery := query.GetStepByID(context, idnumber)
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
		"code":   http.StatusOK,
		"result": step,
	})
}
func GetStepByJobID(ctx *gin.Context) {
	context := ctx.Request.Context()

	jobID := ctx.Param("id")

	if jobID == "" {
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

	idnumber, _ := strconv.Atoi(jobID)
	logger.Infof("get step with id %d", idnumber)

	step, errorDBQuery := query.GetStepByID(context, idnumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when updating job, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when updating job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   http.StatusOK,
		"result": step,
	})
}
func UpdateStepByID(ctx *gin.Context) {

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
	logger.Infof("updating step with id %d", idnumber)
	var updateStepByIDParams db.UpdateStepByIDParams
	err := ctx.BindJSON(&updateStepByIDParams)
	if err != nil {
		logger.Errorf("invalid request, error message is %s", err)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "invalid request",
			"code":   http.StatusBadRequest,
		})
		return
	}
	updateStepByIDParams.ID = idnumber
	step, errorDBQuery := query.UpdateStepByID(context, updateStepByIDParams)
	if errorDBQuery != nil {
		logger.Errorf("Error when updating job, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when updating job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   http.StatusOK,
		"result": step,
	})
}
func UpdateStepByJobID(ctx *gin.Context) {

}
func DeleteStepByID(ctx *gin.Context) {

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
	logger.Infof("deleting step with id %d", idnumber)
	step, errorDBQuery := query.DeleteStepByID(context, idnumber)
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
		"code":   http.StatusOK,
		"result": step.ID,
	})
}
func DeleteStepByJobID(ctx *gin.Context) {
}
