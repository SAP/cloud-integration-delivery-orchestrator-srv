package main

import (
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/env"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/handler"
)

var logger = env.Logger().Desugar()

func main() {
	//engine := gin.New()
	router := gin.New()
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))
	// router.Use(cors.Default()) // allow all origins
	router.Use(AuthMiddleware())

	v1Group := router.Group("/api/v1")
	{
		v1Group.GET("/count", handler.JobCounts)
		v1Group.GET("/job", handler.GetJobsByType)
		v1Group.POST("/job", handler.CreateJobHandler)
		v1Group.POST("job/copy/:id", handler.CopyJob)
		v1Group.DELETE("/job/:id", handler.DeleteJob)
		v1Group.PUT("/job", handler.UpSertJobWithStep) // update or insert steps within a job
		v1Group.POST("/job/:id", handler.ExecuteJob)
		v1Group.GET("/job/:id", handler.GetJobAndStepsByID)

		v1Group.GET("/tanant/packages", handler.GetPackagesHandler)                   //get all packages under a tenant
		v1Group.GET("/tenant/packages/artifacts", handler.GetPackageArtifactsHandler) // get all iflows under a package
		v1Group.GET("/tenant/runtime", handler.GetRuntimeArtifacts)                   // get all runtime artifacts under a tenant

		v1Group.GET("/tms/nodes", handler.GetTmsNodesHandler)
		v1Group.GET("/tms/trs", handler.GetTranportRequestsHandler)

		v1Group.DELETE("/step", handler.DeleteStep)

		v1Group.GET("/destinations", handler.GetDestinationsHandler) // get cpi tenant destinations
		// transport plan
		v1Group.POST("/parse", handler.ParseYaml)
		v1Group.POST("/transportplan/generate/import", handler.GenerateImportJob)
		v1Group.POST("/transportplan/generate/deploy", handler.GenerateDeployJob)
		v1Group.GET("/transportplan/:id", handler.GetTransportPlan)
		v1Group.GET("/transportplan", handler.GetAllTransportPlans)
		v1Group.POST("/transportplan", handler.SaveTransportPlan)
		v1Group.DELETE("/transportplan/:id", handler.DeleteTransportPlan)

		v1Group.GET("/transportGroup", handler.GetTransportGroups)
		v1Group.POST("/transportGroup", handler.CreateTransportGroup)
		v1Group.DELETE("/transportGroup", handler.DeleteTransportGroup)
	}

	if err := router.Run(":8080"); err != nil {
		panic(err)
	}

}
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Do some authentication here
		if len(c.Request.Header["X-User-Email"]) == 0 {
			c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized. Please provide X-User-Email header"})
			return
		}
		email := c.Request.Header["X-User-Email"][0]
		c.Set("user", email)
		c.Next()
	}
}
