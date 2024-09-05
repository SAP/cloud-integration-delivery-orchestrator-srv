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

		v1Group.GET("/apiEndpoint", handler.GetApiEndpoints)
		v1Group.POST("/apiEndpoint", handler.CreateApiEndpoint)
		v1Group.PUT("/apiEndpoint/:id", handler.UpdateApiEndpoint)
		v1Group.DELETE("/apiEndpoint/:id", handler.DeleteApiEndpoint)
		v1Group.GET("/apiEndpoint/:id", handler.GetApiEndpointById)
		v1Group.GET("/apiEndpoints", handler.GetEndpointsByType)

		v1Group.GET("/job", handler.GetJobs)
		v1Group.POST("/job", handler.CreateJob)
		v1Group.DELETE("/job/:id", handler.DeleteJob)
		v1Group.PUT("/job", handler.UpSertJobWithStep)
		v1Group.POST("/job/:id", handler.ExecuteJob)
		v1Group.GET("/job/:id", handler.GetJobByID)

		v1Group.GET("/step", handler.GetSteps)
		v1Group.POST("/step", handler.CreateStep)
		v1Group.DELETE("/step/:id", handler.DeleteStepByID)
		v1Group.DELETE("/step/job/:id", handler.DeleteStepByJobID)
		v1Group.PUT("/step/:id", handler.UpdateStepByID)
		v1Group.PUT("/step/job", handler.UpdateStepByID)
		v1Group.GET("/step/:id", handler.GetStepByID)
		v1Group.GET("/step/job/id", handler.GetStepByJobID)

		v1Group.GET("/tanant/packages", handler.GetPackagesHandler)             //get all packages under a tenant
		v1Group.GET("/tenant/packages/iflows", handler.GetPackageIflowsHandler) // get all iflows under a package

		v1Group.GET("/tms/nodes", handler.GetTmsNodesHandler)
		v1Group.GET("/tms/trs", handler.GetTranportRequestsHandler)

		v1Group.POST("/importStep", handler.CreateImportStep)
		v1Group.POST("/deployStep", handler.CreateDeployStep)

	}

	if err := router.Run(":9000"); err != nil {
		panic(err)
	}

}
