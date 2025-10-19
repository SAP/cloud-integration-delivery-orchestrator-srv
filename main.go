package main

import (
	"time"

	"mmt-delivery/pkg/env"

	"mmt-delivery/handler"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
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
		// tms
		v1Group.GET("/tms/nodes", handler.GetTmsNodesHandler)
		v1Group.GET("/tms/trs", handler.GetTranportRequestsHandler)
		v1Group.GET("/tms/routes", handler.GetRoutesHandler)

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
		// transport group
		v1Group.GET("/transportGroup", handler.GetTransportGroups)
		v1Group.POST("/transportGroup", handler.CreateTransportGroup)
		v1Group.DELETE("/transportGroup", handler.DeleteTransportGroup)
		// cpi tenant bind
		v1Group.GET("/cpiTenant", handler.GetCpiTenants)
		v1Group.GET("/cpiTenant/:id", handler.GetCpiTenant)
		v1Group.POST("/cpiTenant", handler.UpsertCpiTenant)
		v1Group.DELETE("/cpiTenant/:id", handler.DeleteCpiTenant)
		// delivery rule
		v1Group.GET("/deliveryRule", handler.GetDeliveryRules)
		v1Group.GET("/deliveryRule/:id", handler.GetDeliveryRule)
		v1Group.POST("/deliveryRule", handler.UpsertDeliveryRule)
		v1Group.DELETE("/deliveryRule/:id", handler.DeleteDeliveryRule)
		// delivery request
		v1Group.GET("/deliveryRequest", handler.GetAllDr)
		v1Group.GET("/deliveryRequest/:id", handler.GetDeliveryRequest)
		v1Group.POST("/deliveryRequest", handler.CreateDr)
		v1Group.PUT("/deliveryRequest", handler.UpdateDr)
		v1Group.DELETE("/deliveryRequest/:id", handler.DeleteDr)
		v1Group.POST("/deliveryRequest/import", handler.HandleImportOps)
		v1Group.POST("/deliveryRequest/deploy", handler.HandleDeployOps)
		v1Group.POST("/deliveryRequest/syncState/:deliveryRequestId", handler.HandleSyncState)

		// tenant group
		v1Group.GET("/tenantGroup/:id", handler.GetTenantGroupByID)
		v1Group.GET("/tenantGroups", handler.ListTenantGroups)
		v1Group.POST("/tenantGroup", handler.CreateTenantGroup)
		v1Group.PUT("/tenantGroup", handler.UpdateTenantGroup)
		v1Group.DELETE("/tenantGroup/:id", handler.DeleteTenantGroup)

	}

	v2Group := router.Group("/api/v2")
	{
		v2Group.POST("/deliver", handler.NativeDeliver)
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
