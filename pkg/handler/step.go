package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

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

func DeleteStep(ctx *gin.Context) {
	stepId, err := strconv.Atoi(ctx.Query("id"))
	stepType := ctx.Query("type")
	if err != nil || stepType == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("bad request. step id:%s, step type: %s", err, stepType),
		})
		return
	}
	client, err := NewDBClient(ctx)
	if err != nil {
		return
	}
	if stepType == "Deploy" {
		err = client.Query.DeleteDeployStepById(ctx, stepId)
	} else if stepType == "Import" {
		err = client.Query.DeleteImportStepById(ctx, stepId)
	}
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"msg": fmt.Sprintf("error deleting %s step %d: %s", stepType, stepId, err),
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": stepId,
	})
}
