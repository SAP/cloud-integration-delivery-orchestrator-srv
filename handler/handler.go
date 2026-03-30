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
// Routes are grouped by required RBAC scope (Viewer / Operator / Admin).
func (h *Handler) SetupRoutes(v1 *gin.RouterGroup, v2 *gin.RouterGroup, requireScope func(string) gin.HandlerFunc) {
	// --- Viewer (read-only) — 22 endpoints ---
	viewer := v1.Group("")
	viewer.Use(requireScope("Viewer"))
	{
		// CPI tenant artifacts
		viewer.GET("/tanant/packages", h.GetPackagesHandler)
		viewer.GET("/tenant/packages/artifacts", h.GetPackageArtifactsHandler)
		viewer.GET("/tenant/runtime", h.GetRuntimeArtifacts)

		// TMS
		viewer.GET("/tms/nodes", h.GetTmsNodesHandler)
		viewer.GET("/tms/trs", h.GetTranportRequestsHandler)
		viewer.GET("/tms/routes", h.GetRoutesHandler)

		// Destinations
		viewer.GET("/destinations", h.GetDestinationsHandler)

		// CPI Tenant (read)
		viewer.GET("/cpiTenant", h.GetCpiTenants)
		viewer.GET("/cpiTenant/:id", h.GetCpiTenant)

		// Delivery Rule (read)
		viewer.GET("/deliveryRule", h.GetDeliveryRules)
		viewer.GET("/deliveryRule/:id", h.GetDeliveryRule)

		// Delivery Request (read)
		viewer.GET("/deliveryRequest", h.GetAllDr)
		viewer.GET("/deliveryRequest/:id", h.GetDeliveryRequest)

		// Counts
		viewer.GET("/deliveryRequest/counts", h.DeliveryRequestCounts)
		viewer.GET("/cpiTenant/counts", h.CpiTenantCounts)
		viewer.GET("/deliveryRule/counts", h.DeliveryRuleCounts)
		viewer.GET("/versionCompare/counts", h.VersionCompareCountsHandler)

		// Version Compare (read)
		viewer.GET("/deliveryRule/:id/versionCompare", h.QueryVersionCompareHandler)
		viewer.GET("/versionCompare/summary", h.VersionCompareSummaryHandler)
		viewer.GET("/versionCompare/includedPackages", h.IncludedPackagesHandler)
		viewer.GET("/deliveryRule/:id/versionCompare/previewDR", h.HandlePreviewDRFromMismatch)

		// Events (SSE)
		viewer.GET("/events", h.SSEHandler)
	}

	// --- Operator (delivery lifecycle) — 18 endpoints ---
	operator := v1.Group("")
	operator.Use(requireScope("Operator"))
	{
		// Delivery Request CRUD & operations
		operator.POST("/deliveryRequest", h.CreateDr)
		operator.PUT("/deliveryRequest", h.UpdateDr)
		operator.DELETE("/deliveryRequest/:id", h.DeleteDr)
		operator.POST("/deliveryRequest/import", h.HandleImportOps)
		operator.POST("/deliveryRequest/deploy", h.HandleDeployOps)
		operator.POST("/deliveryRequest/syncState/:deliveryRequestId", h.HandleSyncState)
		operator.POST("/deliveryRequest/deleteOps", h.HandleDeleteOps)
		operator.POST("/deliveryRequest/insertOps", h.HandleInsertOps)
		operator.PUT("/deliveryRequest/updateOps", h.HandleUpdateOps)
		operator.POST("/deliveryRequest/checkTr", h.HandleCheckTr)

		// Approval
		operator.POST("/deliveryRequest/requestApproval", h.HandleRequestApproval)
		operator.POST("/deliveryRequest/approve", h.HandleApproveDeliveryRequest)

		// Cancel
		operator.POST("/deliveryRequest/cancel", h.HandleCancelDr)

		// UAA (needed for approval flow)
		operator.GET("/uaa/search/:email", h.HandleUaaUserEmailSearch)
		operator.GET("/uaa/id/:id", h.HandleUaaUserIDSearch)

		// Version Compare (write operations)
		operator.POST("/deliveryRule/:id/versionCompare/trigger", h.TriggerVersionCompareHandler)
		operator.PUT("/versionCompare/includedPackages", h.UpdateIncludedPackagesHandler)
		operator.POST("/deliveryRule/:id/versionCompare/createDR", h.HandleCreateDRFromMismatch)
	}

	// --- Admin (system configuration) — 5 endpoints ---
	admin := v1.Group("")
	admin.Use(requireScope("Admin"))
	{
		// CPI Tenant management
		admin.POST("/cpiTenant", h.UpsertCpiTenant)
		admin.DELETE("/cpiTenant/:id", h.DeleteCpiTenant)

		// Delivery Rule management
		admin.POST("/deliveryRule", h.UpsertDeliveryRule)
		admin.DELETE("/deliveryRule/:id", h.DeleteDeliveryRule)
		admin.POST("/deliveryRule/ruleCheck", h.RuleCheck)
	}

	// --- v2 API (Operator scope) — 1 endpoint ---
	v2Operator := v2.Group("")
	v2Operator.Use(requireScope("Operator"))
	{
		v2Operator.POST("/deliver", h.NativeDeliver)
	}
}
