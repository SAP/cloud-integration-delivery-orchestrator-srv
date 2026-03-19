package handler

import (
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/tms"
	"mmt-delivery/pkg/xsuaa"
	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Handler holds all injected dependencies for the HTTP handler layer.
// All gin handler functions are methods on this struct.
type Handler struct {
	svc          *service.Service
	db           *gorm.DB
	logger       *zap.SugaredLogger
	tms          *tms.TmsClient
	cpi          *cpi.Manager
	xsuaa        *xsuaa.UaaClient
	destinations map[string]env.Destination
	eventBus     *service.EventBus
}

type StatusCount struct {
	Total        int64
	StatusCounts map[string]uint
}

func NewHandler(
	svc *service.Service,
	db *gorm.DB,
	logger *zap.SugaredLogger,
	tmsClient *tms.TmsClient,
	cpiManager *cpi.Manager,
	xsuaaClient *xsuaa.UaaClient,
	destinations map[string]env.Destination,
	eventBus *service.EventBus,
) *Handler {
	return &Handler{
		svc:          svc,
		db:           db,
		logger:       logger,
		tms:          tmsClient,
		cpi:          cpiManager,
		xsuaa:        xsuaaClient,
		destinations: destinations,
		eventBus:     eventBus,
	}
}

// SetupRoutes registers all API routes on the given router group.
func (h *Handler) SetupRoutes(v1 *gin.RouterGroup, v2 *gin.RouterGroup) {
	// CPI tenant artifacts
	v1.GET("/tanant/packages", h.GetPackagesHandler)
	v1.GET("/tenant/packages/artifacts", h.GetPackageArtifactsHandler)
	v1.GET("/tenant/runtime", h.GetRuntimeArtifacts)

	// TMS
	v1.GET("/tms/nodes", h.GetTmsNodesHandler)
	v1.GET("/tms/trs", h.GetTranportRequestsHandler)
	v1.GET("/tms/routes", h.GetRoutesHandler)

	// Destinations
	v1.GET("/destinations", h.GetDestinationsHandler)

	// CPI tenant CRUD
	v1.GET("/cpiTenant", h.GetCpiTenants)
	v1.GET("/cpiTenant/:id", h.GetCpiTenant)
	v1.POST("/cpiTenant", h.UpsertCpiTenant)
	v1.DELETE("/cpiTenant/:id", h.DeleteCpiTenant)

	// Delivery rule
	v1.GET("/deliveryRule", h.GetDeliveryRules)
	v1.GET("/deliveryRule/:id", h.GetDeliveryRule)
	v1.POST("/deliveryRule", h.UpsertDeliveryRule)
	v1.DELETE("/deliveryRule/:id", h.DeleteDeliveryRule)
	v1.POST("/deliveryRule/ruleCheck", h.RuleCheck)

	// Delivery request
	v1.GET("/deliveryRequest", h.GetAllDr)
	v1.GET("/deliveryRequest/:id", h.GetDeliveryRequest)
	v1.POST("/deliveryRequest", h.CreateDr)
	v1.PUT("/deliveryRequest", h.UpdateDr)
	v1.DELETE("/deliveryRequest/:id", h.DeleteDr)
	v1.POST("/deliveryRequest/import", h.HandleImportOps)
	v1.POST("/deliveryRequest/deploy", h.HandleDeployOps)
	v1.POST("/deliveryRequest/syncState/:deliveryRequestId", h.HandleSyncState)
	v1.POST("/deliveryRequest/deleteOps", h.HandleDeleteOps)
	v1.POST("/deliveryRequest/insertOps", h.HandleInsertOps)
	v1.PUT("/deliveryRequest/updateOps", h.HandleUpdateOps)
	v1.POST("/deliveryRequest/checkTr", h.HandleCheckTr)

	// Approval
	v1.POST("/deliveryRequest/requestApproval", h.HandleRequestApproval)
	v1.POST("/deliveryRequest/approve", h.HandleApproveDeliveryRequest)

	// Cancel
	v1.POST("/deliveryRequest/cancel", h.HandleCancelDr)

	// UAA
	v1.GET("/uaa/search/:email", h.HandleUaaUserEmailSearch)
	v1.GET("/uaa/id/:id", h.HandleUaaUserIDSearch)

	// Counts
	v1.GET("/deliveryRequest/counts", h.DeliveryRequestCounts)
	v1.GET("/cpiTenant/counts", h.CpiTenantCounts)
	v1.GET("/deliveryRule/counts", h.DeliveryRuleCounts)
	v1.GET("/versionCompare/counts", h.VersionCompareCountsHandler)

	// Version Compare
	v1.POST("/deliveryRule/:id/versionCompare/trigger", h.TriggerVersionCompareHandler)
	v1.GET("/deliveryRule/:id/versionCompare", h.QueryVersionCompareHandler)
	v1.GET("/versionCompare/summary", h.VersionCompareSummaryHandler)
	v1.GET("/versionCompare/includedPackages", h.IncludedPackagesHandler)
	v1.PUT("/versionCompare/includedPackages", h.UpdateIncludedPackagesHandler)

	// Auto-Create DR from Version Compare Mismatch
	v1.GET("/deliveryRule/:id/versionCompare/previewDR", h.HandlePreviewDRFromMismatch)
	v1.POST("/deliveryRule/:id/versionCompare/createDR", h.HandleCreateDRFromMismatch)

	// Events
	v1.GET("/events", h.SSEHandler)

	// v2
	v2.POST("/deliver", h.NativeDeliver)
}
