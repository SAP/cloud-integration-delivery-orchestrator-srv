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
		v1Group.GET("/job", handler.GetJobs)
		v1Group.POST("/job", handler.CreateJob)
		v1Group.DELETE("/job/:id", handler.DeleteJob)
		v1Group.PUT("/job", handler.UpSertJobWithStep) // update or insert steps within a job
		v1Group.POST("/job/:id", handler.ExecuteJob)
		v1Group.GET("/job/:id", handler.GetJobByID)

		v1Group.GET("/tanant/packages", handler.GetPackagesHandler)             //get all packages under a tenant
		v1Group.GET("/tenant/packages/iflows", handler.GetPackageIflowsHandler) // get all iflows under a package

		v1Group.GET("/tms/nodes", handler.GetTmsNodesHandler)
		v1Group.GET("/tms/trs", handler.GetTranportRequestsHandler)

		v1Group.DELETE("/step", handler.DeleteStep)
		v1Group.POST("/importStep", handler.CreateImportStep)
		v1Group.POST("/deployStep", handler.CreateDeployStep)

		v1Group.GET("/destinations", handler.GetDestinationsHandler)
	}

	if err := router.Run(":9000"); err != nil {
		panic(err)
	}

}
