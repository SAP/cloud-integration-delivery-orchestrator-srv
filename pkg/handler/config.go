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
	var config db.Config
	var configs []db.Config
	var errorConfigRetrieve error
	configType := ctx.Query("type")
	id := ctx.Query("id")

	if configType == "" && id == "" {
		logger.Infof("getting config without id and type")
		configs, errorConfigRetrieve = query.GetConfigsAll(context)
		if errorConfigRetrieve != nil {
			logger.Errorf("Error when retrieve config from database, error message is %s", errorConfigRetrieve)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when retrieve config from database",
				"code":   http.StatusServiceUnavailable,
			})
			return
		}

	}
	if id != "" {
		idnumber, _ := strconv.Atoi(id)
		logger.Infof("getting config with id %d", idnumber)
		config, errorConfigRetrieve = query.GetConfigByID(context, int32(idnumber))
		if errorConfigRetrieve != nil {
			logger.Errorf("Error when retrieve config from database, error message is %s", errorConfigRetrieve)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when retrieve config from database",
				"code":   http.StatusServiceUnavailable,
			})
			return
		}
		configs = append(configs, config)
	}
	if id == "" && configType != "" {
		logger.Infof("getting config with type")
		configs, errorConfigRetrieve = query.GetConfigsByType(context, configType)
		if errorConfigRetrieve != nil {
			logger.Errorf("Error when retrieve config from database, error message is %s", errorConfigRetrieve)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "failed",
				"msg":    "Error when retrieve config from database",
				"code":   http.StatusServiceUnavailable,
			})
			return

		}
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
		configResp, error2 := query.CreateConfig(context, config)
		if error2 != nil {
			logger.Errorf("Error when retrieve config from database, error message is %s", error2)
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
	id := ctx.Query("id")
	if id == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"status": "failed",
			"msg":    "no id available, please specify the id of config",
			"code":   http.StatusBadRequest,
		})
		return
	}
	idnumber, _ := strconv.Atoi(id)

	config, error2 := query.DeleteConfigByID(context, int32(idnumber))
	if error2 != nil {
		logger.Errorf("Error when retrieve config from database, error message is %s", error2)
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
