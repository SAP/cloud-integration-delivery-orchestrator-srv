package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/env"

	"mmt-delivery/handler"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/jwk"
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
		v1Group.POST("/deliveryRequest/deleteOps", handler.HandleDeleteOps)
		v1Group.POST("/deliveryRequest/insertOps", handler.HandleInsertOps) // batch delete
		v1Group.PUT("/deliveryRequest/updateOps", handler.HandleUpdateOps)
		// approve
		v1Group.POST("/deliveryRequest/requestApproval", handler.HandleRequestApproval)
		v1Group.POST("/deliveryRequest/approve", handler.HandleApproveDeliveryRequest)

		// uaa
		v1Group.GET("/uaa/search/:email", handler.HandleUaaUserSearch)

	}

	v2Group := router.Group("/api/v2")
	{
		v2Group.POST("/deliver", handler.NativeDeliver)
	}

	if err := router.Run(":8080"); err != nil {
		panic(err)
	}

}

func keyFromJKU(jku string, kid string) (*rsa.PublicKey, error) {
	set, err := jwk.Fetch(context.Background(), jku)
	if err != nil {
		return nil, err
	}
	key, ok := set.LookupKeyID(kid)
	if !ok {
		return nil, fmt.Errorf("kid %s not found in JWKS", kid)
	}
	var rsaPubKey rsa.PublicKey
	if err := key.Raw(&rsaPubKey); err != nil {
		return nil, err
	}
	return &rsaPubKey, nil
}
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		token, err := jwt.ParseWithClaims(tokenStr, &db.UaaClaims{}, func(t *jwt.Token) (any, error) {
			jku, _ := t.Header["jku"].(string)
			kid, _ := t.Header["kid"].(string)
			if jku == "" || kid == "" {
				return nil, fmt.Errorf("missing jku or kid in header")
			}
			return keyFromJKU(jku, kid)
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(403, gin.H{"error": "invalid token:" + err.Error()})
			return
		}
		claims := token.Claims.(*db.UaaClaims)
		c.Set("user_name", claims.UserName)
		c.Set("scope", claims.Scope)
		c.Set("uaa_claim", *claims)
		c.Next()
	}
}
