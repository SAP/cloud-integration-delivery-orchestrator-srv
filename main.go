package main

import (
	"net/http"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/handler"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
)

func RootHandler(ctx *gin.Context) {
	ctx.HTML(http.StatusOK, "index/index.html", "maco-deploy")

}

func main() {
	//engine := gin.New()
	router := gin.New()
	var logger = log.NewLogger()
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))
	router.LoadHTMLGlob("templates/**/*")
	router.Static("/static", "static")
	router.GET("/", RootHandler)
	v1Group := router.Group("/api/v1")
	{
		v1Group.GET("/users", handler.GetUserInfoHandler)
		v1Group.GET("/user/:name", handler.GetUsersHandler)
		v1Group.POST("/user", handler.CreateUsersHandler)

		v1Group.GET("/config", handler.GetConfigs)
		v1Group.POST("/config", handler.CreateConfig)
		v1Group.PUT("/config/:id", handler.UpdateConfig)
		v1Group.DELETE("/config/:id", handler.DeleteConfig)
		v1Group.GET("/config/:id", handler.GetConfigbyID)

		v1Group.GET("/job", handler.GetJobs)
		v1Group.POST("/job", handler.CreateJob)
		v1Group.DELETE("/job/:id", handler.DeleteJob)
		v1Group.PUT("/job/:id", handler.UpdateJob)
		v1Group.GET("/job/:id", handler.GetJobyID)

	}

	if err := router.Run(":9000"); err != nil {
		panic(err)
	}

}
