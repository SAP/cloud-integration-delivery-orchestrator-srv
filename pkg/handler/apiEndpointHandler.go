package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

func GetApiEndpoints(ctx *gin.Context) {
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

	logger.Info("getting all configs")
	configs, errorDBQuery := query.GetApiEndpointsAll(context)
	if errorDBQuery != nil {
		logger.Errorf("Error when retrieve config from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}

	var maskConfig []db.ApiEndpoint
	for _, conf := range configs {
		conf.ClientSecret = "encrypted"
		maskConfig = append(maskConfig, conf)
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": maskConfig,
	})
}

func GetApiEndpointById(ctx *gin.Context) {

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
	config, errorDBQuery := query.GetApiEndpointById(context, idnumber)
	if errorDBQuery != nil {
		logger.Errorf("Error when retrieve config from database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	config.ClientSecret = "encrypted"
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": config,
	})
}

func GetEndpointsByType(ctx *gin.Context) {
	dbClient := NewDBClient(ctx)
	tms_tenant := ctx.Query("type")
	query := db.New(dbClient.DBConn)
	apiEndpoints, error := query.GetApiEndpointsByType(ctx, tms_tenant)
	if error != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{
			"status": 500,
			"result": fmt.Sprintf("internal server error: %s", error),
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": 200,
		"result": apiEndpoints,
	})
}

func CreateApiEndpoint(ctx *gin.Context) {
	context := ctx.Request.Context()
	var config db.CreateApiendpointParams
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
	configResp, errorDBQuery := query.CreateApiendpoint(context, config)
	if errorDBQuery != nil {
		logger.Errorf("Error when storing config to database, error message is %s", errorDBQuery)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "Error when retrieve config from database",
			"code":   http.StatusServiceUnavailable,
		})
		return
	}
	var maskConfig = configResp
	maskConfig.ClientSecret = "encrypted"
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": maskConfig,
	})
}

func DeleteApiEndpoint(ctx *gin.Context) {

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

	config, errorDBQuery := query.DeleteApiEndpointById(context, idnumber)
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
func UpdateApiEndpoint(ctx *gin.Context) {

	context := ctx.Request.Context()
	id := ctx.Param("id")
	var config db.UpdateApiEndpointByIdParams

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
		idNumber, _ := strconv.Atoi(id)
		config.ID = idNumber

		configResp, errorDBQuery := query.UpdateApiEndpointById(context, config)
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
			maskConfig.ClientSecret = "encrypted"
			ctx.JSON(http.StatusOK, gin.H{
				"status": "success",
				"code":   200,
				"result": maskConfig,
			})
		}
	}
}
