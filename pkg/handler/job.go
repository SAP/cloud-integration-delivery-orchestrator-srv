package handler

import (
	"net/http"

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

}
