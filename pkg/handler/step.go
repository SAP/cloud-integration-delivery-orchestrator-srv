package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

func DeleteStep(ctx *gin.Context) {
	stepId, err := strconv.Atoi(ctx.Query("id"))
	stepType := ctx.Query("type")
	if err != nil || stepType == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg": fmt.Sprintf("bad request. step id:%s, step type: %s", err, stepType),
		})
		return
	}

	models := map[string]any{
		"Import":   &db.ImportStep{},
		"Deploy":   &db.DeployStep{},
		"Undeploy": nil,
	}

	if err := db.Conn().Delete(models[stepType], stepId).Error; err != nil {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": stepId,
	})
}
