package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	log "github.com/sirupsen/logrus"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

func GetCpiConfig(ctx *gin.Context) {

	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, dbSource)

	if errDBconn != nil {
		log.Printf("bad request, error message is%s", errDBconn)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg":  "failed",
			"code": http.StatusBadRequest,
		})
	} else {
		query := db.New(dbConn)
		if configName := ctx.Query("name"); configName != "" {
			config, error2 := query.GetConfigByName(context, configName)
			if error2 != nil {
				log.Printf("Error when retrieve config from database, error message is %s", error2)
				ctx.JSON(http.StatusServiceUnavailable, gin.H{
					"msg":  "failed",
					"code": http.StatusServiceUnavailable,
				})
			} else {
				var maskConfig = config
				maskConfig.AuthClientSecret = "encrypted"
				ctx.JSON(http.StatusOK, gin.H{
					"msg":    "success",
					"code":   200,
					"result": maskConfig,
				})
			}
		}
		if id := ctx.Query("id"); id != "" {
			idnumber, _ := strconv.Atoi(id)
			config, error2 := query.GetConfigByID(context, int32(idnumber))
			if error2 != nil {
				log.Printf("Error when retrieve config from database, error message is %s", error2)
				ctx.JSON(http.StatusServiceUnavailable, gin.H{
					"msg":  "failed",
					"code": http.StatusServiceUnavailable,
				})
			} else {
				var maskConfig = config
				maskConfig.AuthClientSecret = "encrypted"
				ctx.JSON(http.StatusOK, gin.H{
					"msg":    "success",
					"code":   200,
					"result": maskConfig,
				})
			}
		}

	}

}

func GetCpiConfigs(ctx *gin.Context) {

	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, dbSource)
	if errDBconn != nil {
		log.Printf("bad request, error message is %s", errDBconn)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg":  "failed",
			"code": http.StatusBadRequest,
		})
	} else {
		query := db.New(dbConn)
		configs, error2 := query.GetConfigs(context, "cpi")
		if error2 != nil {
			log.Printf("Error when retrieve config from database, error message is %s", error2)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"msg":  "failed",
				"code": http.StatusServiceUnavailable,
			})
		} else {
			var maskConfig []db.Config
			for _, conf := range configs {
				conf.AuthClientSecret = "encrypted"
				maskConfig = append(maskConfig, conf)
			}
			ctx.JSON(http.StatusOK, gin.H{
				"msg":    "success",
				"code":   200,
				"result": maskConfig,
			})
		}
	}

}
func CreateCpiConfig(ctx *gin.Context) {

	context := ctx.Request.Context()
	var config db.CreateConfigParams
	err := ctx.BindJSON(&config)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg":  "failed",
			"code": http.StatusBadRequest,
		})
		log.Printf("invalid request, error message is %s", err)
	} else {
		config.Type = "cpi"
		dbConn, errDBconn := pgx.Connect(context, dbSource)
		if errDBconn != nil {
			log.Printf("error connecting to db, error message is %s", errDBconn)
		}
		query := db.New(dbConn)
		configResp, error2 := query.CreateConfig(context, config)
		if error2 != nil {
			log.Printf("Error when retrieve config from database, error message is %s", error2)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{
				"msg":  "faild",
				"code": http.StatusServiceUnavailable,
			})
		} else {
			var maskConfig = configResp
			maskConfig.AuthClientSecret = "encrypted"
			ctx.JSON(http.StatusOK, gin.H{
				"msg":    "success",
				"code":   200,
				"result": maskConfig,
			})
		}
	}
}

func DeleteCpiConfig(ctx *gin.Context) {

	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, dbSource)
	if errDBconn != nil {
		log.Printf("bad request, error message is %s", errDBconn)
		ctx.JSON(http.StatusBadRequest, gin.H{
			"msg":  "failed",
			"code": http.StatusBadRequest,
		})
	} else {
		query := db.New(dbConn)
		if id := ctx.Query("id"); id != "" {
			idnumber, _ := strconv.Atoi(id)
			config, error2 := query.DeleteConfigByID(context, int32(idnumber))
			if error2 != nil {
				log.Printf("Error when retrieve config from database, error message is %s", error2)
				ctx.JSON(http.StatusServiceUnavailable, gin.H{
					"msg":  "failed",
					"code": http.StatusServiceUnavailable,
				})
			} else {
				ctx.JSON(http.StatusOK, gin.H{
					"msg":  "success",
					"code": 200,
					"id":   config.ID,
				})
			}
		}

	}
}
