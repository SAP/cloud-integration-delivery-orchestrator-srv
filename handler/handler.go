package handler

import (
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/xsuaa"
	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Handler holds all injected dependencies for the HTTP handler layer.
// All gin handler functions are methods on this struct.
type Handler struct {
	svc      *service.Service
	db       *gorm.DB
	logger   *zap.SugaredLogger
	cpi      *cpi.Manager
	xsuaa    *xsuaa.UaaClient
	destSvc  *cf.DestinationServiceClient
	eventBus *service.EventBus
}

type StatusCount struct {
	Total        int64
	StatusCounts map[string]uint
}

// --- Response helpers ---

// OK sends a 200 response with data payload.
func OK(c *gin.Context, data any) {
	c.JSON(200, gin.H{"data": data})
}

// OKMsg sends a 200 response with data + a toast message.
func OKMsg(c *gin.Context, data any, message string) {
	c.JSON(200, gin.H{"data": data, "message": message})
}

// Fail sends an error response with message.
func Fail(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"message": message})
}

// FailErrors sends an error response with message + structured error details.
func FailErrors(c *gin.Context, status int, message string, errors any) {
	c.AbortWithStatusJSON(status, gin.H{"message": message, "errors": errors})
}

func NewHandler(
	svc *service.Service,
	db *gorm.DB,
	logger *zap.SugaredLogger,
	cpiManager *cpi.Manager,
	xsuaaClient *xsuaa.UaaClient,
	destSvc *cf.DestinationServiceClient,
	eventBus *service.EventBus,
) *Handler {
	return &Handler{
		svc:      svc,
		db:       db,
		logger:   logger,
		cpi:      cpiManager,
		xsuaa:    xsuaaClient,
		destSvc:  destSvc,
		eventBus: eventBus,
	}
}

// SetupRoutes registers all API routes on the given router group.
// Routes are grouped by required RBAC scope (fine-grained, resource-based).
func (h *Handler) SetupRoutes(v1 *gin.RouterGroup, v2 *gin.RouterGroup, requireScope func(string) gin.HandlerFunc) {

	// --- No scope required (any authenticated user) ---
	v1.GET("/currentUser/scopes", h.GetCurrentUserScopes)

	// --- Integration.Read — CPI artifacts, TMS, Destinations, SSE ---
	integrationRead := v1.Group("")
	integrationRead.Use(requireScope("Integration.Read"))
	{
		integrationRead.GET("/tanant/packages", h.GetPackagesHandler)
		integrationRead.GET("/tenant/packages/artifacts", h.GetPackageArtifactsHandler)
		integrationRead.GET("/tenant/runtime", h.GetRuntimeArtifacts)
		integrationRead.GET("/tms/nodes", h.GetTmsNodesHandler)
		integrationRead.GET("/tms/trs", h.GetTranportRequestsHandler)
		integrationRead.GET("/tms/routes", h.GetRoutesHandler)
		integrationRead.GET("/destinations", h.GetDestinationsHandler)
		integrationRead.GET("/events", h.SSEHandler)
	}

	// --- CpiTenant.Read ---
	tenantRead := v1.Group("")
	tenantRead.Use(requireScope("CpiTenant.Read"))
	{
		tenantRead.GET("/cpiTenant", h.GetCpiTenants)
		tenantRead.GET("/cpiTenant/:id", h.GetCpiTenant)
		tenantRead.GET("/cpiTenant/counts", h.CpiTenantCounts)
	}

	// --- CpiTenant.Manage ---
	tenantManage := v1.Group("")
	tenantManage.Use(requireScope("CpiTenant.Manage"))
	{
		tenantManage.POST("/cpiTenant", h.UpsertCpiTenant)
		tenantManage.PUT("/cpiTenant/:id/cfIdentity", h.SaveCfIdentity)
		tenantManage.DELETE("/cpiTenant/:id", h.DeleteCpiTenant)

		// RFC 013 §09: CF org/space discovery (proxies CF API, token never persisted)
		tenantManage.POST("/cf/token", h.ExchangeCfPasscode)
		tenantManage.POST("/cf/orgs", h.ListCfOrgs)
		tenantManage.POST("/cf/spaces", h.ListCfSpaces)

		// RFC 013: bootstrap lifecycle endpoints
		tenantManage.POST("/cpiTenant/:id/bootstrap/preview", h.PreviewBootstrap)
		tenantManage.POST("/cpiTenant/:id/bootstrap/apply", h.ApplyBootstrap)
		tenantManage.GET("/cpiTenant/:id/bootstrap/status", h.GetBootstrapStatus)
		tenantManage.POST("/cpiTenant/:id/bootstrap/retry", h.RetryBootstrap)
		tenantManage.POST("/cpiTenant/:id/bootstrap/reset", h.ResetBootstrap)

		// RFC 013 Phase 3: TMS Node registration (sync, independent of bootstrap)
		tenantManage.POST("/cpiTenant/:id/tms-node/register", h.RegisterTmsNode)
		tenantManage.GET("/cpiTenant/:id/tms-node/routes", h.GetCurrentNodeRoutes)
		tenantManage.POST("/cpiTenant/:id/tms-node/confirm", h.ConfirmTmsRoutes)

		// RFC 013: central TMS context (admin-level, single record per v1 deployment)
		tenantManage.GET("/centralTmsContext", h.GetCentralTmsContext)
		tenantManage.PUT("/centralTmsContext", h.UpsertCentralTmsContext)

		// RFC 013 Phase 4: CAS-based TR generation
		tenantManage.POST("/cpiTenant/:id/generateTR", h.HandleGenTR)
	}

	// --- DeliveryRule.Read ---
	ruleRead := v1.Group("")
	ruleRead.Use(requireScope("DeliveryRule.Read"))
	{
		ruleRead.GET("/deliveryRule", h.GetDeliveryRules)
		ruleRead.GET("/deliveryRule/:id", h.GetDeliveryRule)
		ruleRead.GET("/deliveryRule/counts", h.DeliveryRuleCounts)
		ruleRead.POST("/deliveryRule/ruleCheck", h.RuleCheck)
	}

	// --- DeliveryRule.Manage ---
	ruleManage := v1.Group("")
	ruleManage.Use(requireScope("DeliveryRule.Manage"))
	{
		ruleManage.POST("/deliveryRule", h.UpsertDeliveryRule)
		ruleManage.DELETE("/deliveryRule/:id", h.DeleteDeliveryRule)
	}

	// --- DeliveryRequest.Read ---
	drRead := v1.Group("")
	drRead.Use(requireScope("DeliveryRequest.Read"))
	{
		drRead.GET("/deliveryRequest", h.GetAllDr)
		drRead.GET("/deliveryRequest/:id", h.GetDeliveryRequest)
		drRead.GET("/deliveryRequest/counts", h.DeliveryRequestCounts)
	}

	// --- DeliveryRequest.Write ---
	drWrite := v1.Group("")
	drWrite.Use(requireScope("DeliveryRequest.Write"))
	{
		drWrite.POST("/deliveryRequest", h.CreateDr)
		drWrite.PUT("/deliveryRequest", h.UpdateDr)
		drWrite.DELETE("/deliveryRequest/:id", h.DeleteDr)
	}

	// --- DeliveryRequest.Operate ---
	drOperate := v1.Group("")
	drOperate.Use(requireScope("DeliveryRequest.Operate"))
	{
		drOperate.POST("/deliveryRequest/import", h.HandleImportOps)
		drOperate.POST("/deliveryRequest/deploy", h.HandleDeployOps)
		drOperate.POST("/deliveryRequest/syncState/:deliveryRequestId", h.HandleSyncState)
		drOperate.POST("/deliveryRequest/deleteOps", h.HandleDeleteOps)
		drOperate.POST("/deliveryRequest/insertOps", h.HandleInsertOps)
		drOperate.PUT("/deliveryRequest/updateOps", h.HandleUpdateOps)
		drOperate.POST("/deliveryRequest/requestApproval", h.HandleRequestApproval)
		drOperate.POST("/deliveryRequest/approve", h.HandleApproveDeliveryRequest)
		drOperate.POST("/deliveryRequest/cancel", h.HandleCancelDr)
		drOperate.GET("/uaa/search/:email", h.HandleUaaUserEmailSearch)
		drOperate.GET("/uaa/id/:id", h.HandleUaaUserIDSearch)
	}

	// --- VersionCompare.Read ---
	vcRead := v1.Group("")
	vcRead.Use(requireScope("VersionCompare.Read"))
	{
		vcRead.GET("/deliveryRule/:id/versionCompare", h.QueryVersionCompareHandler)
		vcRead.GET("/versionCompare/summary", h.VersionCompareSummaryHandler)
		vcRead.GET("/versionCompare/counts", h.VersionCompareCountsHandler)
		vcRead.GET("/versionCompare/includedPackages", h.IncludedPackagesHandler)
		vcRead.GET("/deliveryRule/:id/versionCompare/previewDR", h.HandlePreviewDRFromMismatch)
	}

	// --- VersionCompare.Trigger ---
	vcTrigger := v1.Group("")
	vcTrigger.Use(requireScope("VersionCompare.Trigger"))
	{
		vcTrigger.POST("/deliveryRule/:id/versionCompare/trigger", h.TriggerVersionCompareHandler)
		vcTrigger.PUT("/versionCompare/includedPackages", h.UpdateIncludedPackagesHandler)
		vcTrigger.POST("/deliveryRule/:id/versionCompare/createDR", h.HandleCreateDRFromMismatch)
	}

	// --- VersionCompare.Adhoc ---
	vcAdhoc := v1.Group("")
	vcAdhoc.Use(requireScope("VersionCompare.Adhoc"))
	{
		vcAdhoc.POST("/versionCompare/adhoc", h.AdhocVersionCompare)
	}

	// --- System Configuration (CpiTenant.Manage scope — admin-level) ---
	system := v1.Group("/system")
	system.Use(requireScope("CpiTenant.Manage"))
	{
		system.GET("/integrations", h.GetIntegrations)
		system.PUT("/integrations/:type", h.UpdateIntegration)
		system.GET("/connectivity", h.CheckConnectivity)
	}

	// --- v2 API (DeliveryRequest.Operate) ---
	v2Operate := v2.Group("")
	v2Operate.Use(requireScope("DeliveryRequest.Operate"))
	{
		v2Operate.POST("/deliver", h.NativeDeliver)
	}
}
