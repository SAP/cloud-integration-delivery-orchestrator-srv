package handler

import (
	"fmt"
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
	id := ctx.Query("id")
	step_type := ctx.Query("type")
	if id == "" {
		logger.Error("invalid request, please supply the id in the url")
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
			"msg":    "error when connecting to the database, please contact your system administrator",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	query := db.New(dbConn)

	step_id, _ := strconv.Atoi(id)
	logger.Infof("deleting step with id %d", step_id)

	var err error
	if step_type == "Import" {
		err = query.DeleteImportStepById(ctx, step_id)
	} else if step_type == "Deploy" {
		err = query.DeleteDeployStepById(ctx, step_id)
	} else {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("step type %s, is neither 'Import' nor 'Deploy'", step_type),
		})
		return
	}
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("internal server error while deleting %s step %d", step_type, step_id),
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": step_id,
		"msg":    fmt.Sprintf("%s step %d deleted", step_type, step_id),
	})
}
func DeleteStepByJobID(ctx *gin.Context) {
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
	logger.Infof("deleting step with id %d", idnumber)
	steps, errorDBQuery := query.DeleteStepByJobID(context, idnumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when deleting job, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when deleting job",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	var stepIDList []int
	for _, step := range steps {
		stepIDList = append(stepIDList, step.ID)
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   http.StatusOK,
		"result": stepIDList,
	})
}

func CreateImportStep(ctx *gin.Context) {
	var importStepParams db.InsertImportStepParams
	err := ctx.BindJSON(&importStepParams)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("Invalid request params: %s", err),
		})
		return
	}
	conn, _ := pgx.Connect(ctx, DBSource)
	query := db.New(conn)
	step, err := query.InsertImportStep(ctx, importStepParams)
	if err != nil {
		logger.Error(err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": step,
	})
}

func UpdateImportStep(ctx *gin.Context) {
	var updateImportStepParams db.UpdateImportStepParams
	err := ctx.BindJSON(&updateImportStepParams)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"result": fmt.Sprintf("Invalid request params: %s", err),
		})
		return
	}
	conn, _ := pgx.Connect(ctx, DBSource)
	query := db.New(conn)
	importStep, err := query.UpdateImportStep(ctx, updateImportStepParams)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, fmt.Sprintf("Internal server error: %s", err))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": importStep,
	})
}

func CreateDeployStep(ctx *gin.Context) {
	var deployParams db.InsertDeployStepParams
	err := ctx.BindJSON(&deployParams)
	if err != nil {
		return
	}
	conn, err := pgx.Connect(ctx, DBSource)
	if err != nil {
		logger.Error(err)
		return
	}
	step, err := db.New(conn).InsertDeployStep(ctx, deployParams)
	if err != nil {
		logger.Error(err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": step,
	})
}
