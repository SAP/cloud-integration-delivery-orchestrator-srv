package handler

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cpi"
	gh "github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/github"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/xsuaa"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Handler holds all injected dependencies for the HTTP handler layer.
// All gin handler functions are methods on this struct.
type Handler struct {
	svc         *service.Service
	db          *gorm.DB
	logger      *zap.SugaredLogger
	cpi         *cpi.Manager
	xsuaa       *xsuaa.UaaClient
	destSvc     *cf.DestinationServiceClient
	hub         *service.WSHub
	gitAppState *gh.StateStore
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

// FailCode sends an error response with a machine-readable code + human message.
// The "code" field allows the frontend to classify errors without parsing strings.
func FailCode(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"code": code, "message": message})
}

// FailCodeErrors sends an error response with code + message + structured error details.
func FailCodeErrors(c *gin.Context, status int, code, message string, errors any) {
	c.AbortWithStatusJSON(status, gin.H{"code": code, "message": message, "errors": errors})
}

// RedirectSPA issues a 302 back to the SPA after a browser-facing callback,
// mirroring the OKMsg()/Fail() JSON helpers but for top-level redirect flows.
// Because a 302 has no body, the toast directive rides the query string: the SPA
// reads ?toast=<severity>&msg=<message> and shows it generically (severity is
// "success" or "error"), exactly like OKMsg carries a backend-authored message.
// The frontend never interprets the outcome — the message is authored here, the
// single source of truth. `extra` carries optional action flags (e.g.
// openGitDialog=1) the destination view honors as a normal deep link.
func RedirectSPA(c *gin.Context, path, severity, message string, extra url.Values) {
	if path == "" {
		path = "/"
	}
	q := url.Values{}
	q.Set("toast", severity)
	q.Set("msg", message)
	for k, vs := range extra {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	c.Redirect(http.StatusFound, path+"?"+q.Encode())
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505). Used to translate duplicate-key errors into
// 409 Conflict responses instead of a raw 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func NewHandler(
	svc *service.Service,
	db *gorm.DB,
	logger *zap.SugaredLogger,
	cpiManager *cpi.Manager,
	xsuaaClient *xsuaa.UaaClient,
	destSvc *cf.DestinationServiceClient,
	hub *service.WSHub,
	gitAppState *gh.StateStore,
) *Handler {
	return &Handler{
		svc:         svc,
		db:          db,
		logger:      logger,
		cpi:         cpiManager,
		xsuaa:       xsuaaClient,
		destSvc:     destSvc,
		hub:         hub,
		gitAppState: gitAppState,
	}
}

// HandleWebSocket upgrades the HTTP connection to WebSocket and manages the lifecycle.
func (h *Handler) HandleWebSocket(c *gin.Context) {
	if h.hub == nil {
		Fail(c, http.StatusServiceUnavailable, "WebSocket hub is not available")
		return
	}

	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Origin check is handled by SAP Approuter; backend skips it
	})
	if err != nil {
		h.logger.Warnf("WebSocket accept failed: %s", err)
		return
	}

	wsConn := h.hub.NewConn(conn)
	go wsConn.ReadPump()
	wsConn.WritePump() // blocks until connection closes
}

// SetupRoutes registers all API routes on the given router group.
// Routes are grouped by required RBAC scope (fine-grained, resource-based).
func (h *Handler) SetupRoutes(v1 *gin.RouterGroup, v2 *gin.RouterGroup, requireScope func(string) gin.HandlerFunc) {

	// --- No scope required (any authenticated user) ---
	v1.GET("/currentUser/scopes", h.GetCurrentUserScopes)

	// --- Integration.Read — CPI artifacts, TMS, Destinations, WebSocket ---
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
		integrationRead.GET("/ws", h.HandleWebSocket)
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
		drRead.GET("/operations/history", h.GetOperationsHistory)
		drRead.GET("/operations/history/filters", h.GetOperationsHistoryFilters)
		drRead.GET("/operationConditions/:opId", h.GetOperationConditions)
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

	// --- Code Compare / Git Sync (VersionCompare.Read scope) ---
	ccRead := v1.Group("")
	ccRead.Use(requireScope("VersionCompare.Read"))
	{
		ccRead.GET("/gitSync/snapshots", h.GetGitSnapshots)
		ccRead.GET("/gitSync/snapshots/:id/files", h.GetGitSnapshotFiles)
	}

	// --- Git Sync Trigger (DeliveryRequest.Operate scope) ---
	gitSyncOperate := v1.Group("")
	gitSyncOperate.Use(requireScope("DeliveryRequest.Operate"))
	{
		gitSyncOperate.POST("/gitSync/trigger", h.TriggerGitSync)
		gitSyncOperate.POST("/gitSync/snapshots/:id/invalidate", h.InvalidateGitSnapshot)
	}

	// --- System Configuration (CpiTenant.Manage scope — admin-level) ---
	system := v1.Group("/system")
	system.Use(requireScope("CpiTenant.Manage"))
	{
		system.GET("/jiraConfig", h.GetJiraConfig)
		system.PUT("/jiraConfig", h.UpdateJiraConfig)
		system.POST("/jiraConfig/test", h.TestJiraConnection)
		system.GET("/ansStatus", h.GetAnsStatus)
		system.POST("/ansStatus/test", h.TestAnsConnection)
		system.GET("/gitRepoConfig", h.GetGitRepoConfig)
		system.GET("/gitRepoConfig/providers", h.GetGitProviders)
		system.GET("/gitRepoConfig/owners", h.GetGitOwners)
		system.GET("/gitRepoConfig/repos", h.GetGitRepos)
		system.PUT("/gitRepoConfig", h.UpsertGitRepoConfig)
		system.POST("/gitRepoConfig/test", h.TestGitRepoConnection)
		// GitHub App manifest flow (RFC 010 doc 12 §9). callback/setup are browser
		// redirects from GitHub carrying the SameSite=Lax session cookie (RP-1).
		system.GET("/gitApp/manifest", h.StartGitAppManifest)
		system.GET("/gitApp/callback", h.GitAppManifestCallback)
		system.GET("/gitApp/setup", h.GitAppSetupCallback)
		// App-mode read-back discovery (OP-1): lists repos the installation was granted.
		system.GET("/gitApp/repos", h.GetGitAppRepos)
		// Install-pending helper: install deep-link for a registered-but-not-yet-installed App.
		system.GET("/gitApp/installUrl", h.GetGitAppInstallURL)
		// Exit mechanism (§10): uninstall + delete destination + unbind config, returns the
		// Advanced-page deep-link for the UI-only App-registration deletion.
		system.DELETE("/gitApp", h.GitAppDisconnect)
		system.GET("/database/info", h.GetDatabaseInfo)
		system.GET("/connectivity/database", h.CheckConnectivityDatabase)
		system.GET("/connectivity/tms", h.CheckConnectivityTMS)
		system.GET("/connectivity/tenants", h.CheckConnectivityTenants)
	}

	// --- v2 API (DeliveryRequest.Operate) ---
	v2Operate := v2.Group("")
	v2Operate.Use(requireScope("DeliveryRequest.Operate"))
	{
		// Native delivery route removed — TMS is the standard delivery mechanism.
	}
}
