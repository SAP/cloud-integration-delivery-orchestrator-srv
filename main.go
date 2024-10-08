package main

import (
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/env"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/handler"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/oauth2"
)

var logger = env.Logger().Desugar()

func main() {
	//engine := gin.New()
	router := gin.New()
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))
	v1Group := router.Group("/api/v1")
	{
		v1Group.GET("/job", handler.GetJobsByType)
		v1Group.POST("/job", handler.CreateJob)
		v1Group.DELETE("/job/:id", handler.DeleteJob)
		v1Group.PUT("/job", handler.UpSertJobWithStep) // update or insert steps within a job
		v1Group.POST("/job/:id", handler.ExecuteJob)
		v1Group.GET("/job/:id", handler.GetJobAndStepsByID)

		v1Group.GET("/tanant/packages", handler.GetPackagesHandler)                   //get all packages under a tenant
		v1Group.GET("/tenant/packages/artifacts", handler.GetPackageArtifactsHandler) // get all iflows under a package

		v1Group.GET("/tms/nodes", handler.GetTmsNodesHandler)
		v1Group.GET("/tms/trs", handler.GetTranportRequestsHandler)

		v1Group.DELETE("/step", handler.DeleteStep)

		v1Group.GET("/destinations", handler.GetDestinationsHandler)
	}
	router.GET("/auth", oauth2.OauthCallback)
	router.GET("/login", oauth2.Login)

	if err := router.Run(":9000"); err != nil {
		panic(err)
	}

}
