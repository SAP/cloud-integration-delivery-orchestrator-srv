package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

func GetConfigs(ctx *gin.Context) {

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

	logger.Info("getting all configs")
	configs, errorDBQuery := query.GetConfigsAll(context)
	if errorDBQuery != nil {
		logger.Errorf("Error when retrieve config from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	var maskConfig []db.Config
	for _, conf := range configs {
		conf.AuthClientSecret = "encrypted"
		maskConfig = append(maskConfig, conf)
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": maskConfig,
	})
}

func GetConfigbyID(ctx *gin.Context) {

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
	config, errorDBQuery := query.GetConfigByID(context, int32(idnumber))
	if errorDBQuery != nil {
		logger.Errorf("Error when retrieve config from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	config.AuthClientSecret = "encrypted"
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": config,
	})
}

func CreateConfig(ctx *gin.Context) {

	context := ctx.Request.Context()
	var config db.CreateConfigParams
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
		configResp, errorDBQuery := query.CreateConfig(context, config)
		if errorDBQuery != nil {
			logger.Errorf("Error when storing config to database, error message is %s", errorDBQuery)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when retrieve config from database",
				"code":   http.StatusServiceUnavailable,
			})
			return
		} else {
			var maskConfig = configResp
			maskConfig.AuthClientSecret = "encrypted"
			ctx.JSON(http.StatusOK, gin.H{
				"status": "success",
				"code":   200,
				"result": maskConfig,
			})
		}
	}
}

func DeleteConfig(ctx *gin.Context) {

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

	config, errorDBQuery := query.DeleteConfigByID(context, int32(idnumber))
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
func UpdateConfig(ctx *gin.Context) {

	context := ctx.Request.Context()
	id := ctx.Param("id")
	var config db.UpdateConfigByIDParams

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
		idNumber, _ := strconv.Atoi(id)
		config.ID = int32(idNumber)

		configResp, errorDBQuery := query.UpdateConfigByID(context, config)
		if errorDBQuery != nil {
			logger.Errorf("Error when retrieve config from database, error message is %s", errorDBQuery)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when retrieve config from database",
				"code":   http.StatusServiceUnavailable,
			})
			return
		} else {
			var maskConfig = configResp
			maskConfig.AuthClientSecret = "encrypted"
			ctx.JSON(http.StatusOK, gin.H{
				"status": "success",
				"code":   200,
				"result": maskConfig,
			})
		}
	}
}
