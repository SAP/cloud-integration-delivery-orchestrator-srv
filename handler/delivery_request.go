package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/service"
)

func (h *Handler) CreateDr(c *gin.Context) {
	var dr db.DeliveryRequest
	var err error
	if err = c.ShouldBindJSON(&dr); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if dr.DeliveryRule.ID == 0 {
		Fail(c, http.StatusBadRequest, "missing delivery rule")
		return
	}
	if dr.Name == "" {
		Fail(c, http.StatusBadRequest, "missing name")
		return
	}

	if err := checkJIRA(dr.JiraLink, dr.DeliveryRule); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var rule db.DeliveryRule
	if rule, err = h.svc.GetDeliveryRuleWithAcc(dr.DeliveryRule.ID); err != nil {
		Fail(c, http.StatusInternalServerError, fmt.Errorf("failed to generate target routes and nodes: %s", err).Error())
		return
	}
	dr.SourceTenantID = rule.SourceTenantID
	dr.DeliveryRuleID = rule.ID

	dr.AggregateStatus = lifecycle.AggPending

	user := service.UserID(c)
	dr.CreatedBy, dr.UpdatedBy = user, user

	if err := h.svc.CreateDeliveryRequest(&dr); err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, dr)
}

// update DeliveryRequest, not including ops
func (h *Handler) UpdateDr(c *gin.Context) {
	var dr db.DeliveryRequest
	if err := c.ShouldBindJSON(&dr); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var existing db.DeliveryRequest
	if err := h.db.First(&existing, dr.ID).Error; err != nil {
		Fail(c, http.StatusNotFound, fmt.Sprintf("delivery request id %d not found", dr.ID))
		return
	}
	if existing.AggregateStatus != lifecycle.AggPending && existing.AggregateStatus != lifecycle.AggWaitingApprove {
		Fail(c, http.StatusBadRequest, "only pending or waiting approval delivery request can be updated")
		return
	}
	rule, err := h.svc.GetDeliveryRuleWithAcc(dr.DeliveryRule.ID)
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	user := service.UserID(c)
	// check and update JIRA
	if existing.JiraLink != dr.JiraLink {
		if err := checkJIRA(dr.JiraLink, rule); err != nil {
			Fail(c, http.StatusBadRequest, err.Error())
			return
		}
		existing.JiraLink = dr.JiraLink
		existing.UpdatedBy = user
	}
	if existing.DeliveryRuleID != dr.DeliveryRule.ID {
		// TODO: check artifact ops in this dr. prevent changing rule if ops has different source tenant id
		existing.DeliveryRuleID = dr.DeliveryRule.ID
		dr.UpdatedBy = user
	}
	existing.Name = dr.Name
	if err := h.db.Updates(&existing).Error; err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, dr)
}

// List all DeliveryRequests
func (h *Handler) GetAllDr(c *gin.Context) {
	var drList []db.DeliveryRequest
	if err := h.db.
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Find(&drList).Error; err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, drList)
}

func (h *Handler) DeliveryRequestCounts(ctx *gin.Context) {
	var res StatusCount
	if err := h.db.Model(&db.DeliveryRequest{}).Count(&res.Total).Error; err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	var counts []struct {
		AggregateStatus lifecycle.AggregateStatus
		Count           uint
	}
	if err := h.db.Model(&db.DeliveryRequest{}).
		Select("aggregate_status, count(*) as count").
		Group("aggregate_status").
		Scan(&counts).Error; err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	res.StatusCounts = make(map[string]uint, len(counts))
	for _, c := range counts {
		res.StatusCounts[string(c.AggregateStatus)] = c.Count
	}
	OK(ctx, res)
}

// Get a single DeliveryRequest by id
func (h *Handler) GetDeliveryRequest(c *gin.Context) {
	raw := c.Param("id")
	drID, err := strconv.Atoi(raw)
	if err != nil || drID <= 0 {
		Fail(c, http.StatusBadRequest, "invalid id")
		return
	}
	dr, err := h.svc.QueryDrWithAssociations(uint(drID))
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, dr)
}

// DeleteDr soft-deletes a DeliveryRequest by id.
// Associated ArtifactTenantOperations and Conditions are not explicitly deleted,
// but are effectively inaccessible: all write paths validate the parent DR first
// (h.db.First(&dr, id)), which returns ErrRecordNotFound for soft-deleted records.
// Preload-based read paths also go through the DR and therefore never reach orphaned rows.
func (h *Handler) DeleteDr(c *gin.Context) {
	raw := c.Param("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		Fail(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.svc.DeleteDeliveryRequest(uint(id)); err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, id)
}

// checkJIRA validates JIRA link format
// Expected format: https://jira.tools.sap/browse/<ISSUE-KEY>
func checkJIRA(jira string, rule db.DeliveryRule) error {
	// Check if JIRA is required but not provided
	if jira == "" && rule.RequireJira {
		return fmt.Errorf("jira link is required when delivery rule requires JIRA. Example: https://jira.tools.sap/browse/<ISSUE-KEY>")
	}

	// If JIRA is provided, validate its format
	if jira != "" {
		// Check if it's a valid URL
		if !strings.HasPrefix(jira, "http://") && !strings.HasPrefix(jira, "https://") {
			return fmt.Errorf("jira link must start with http:// or https://. Example: https://jira.tools.sap/browse/<ISSUE-KEY>")
		}

		// Validate JIRA URL format using regex
		// Pattern: https://<domain>/browse/<PROJECT-ID>
		// PROJECT-ID format: one or more uppercase letters, hyphen, one or more digits
		pattern := `^https?://[^/]+/browse/[A-Z]+-\d+$`
		matched, err := regexp.MatchString(pattern, jira)
		if err != nil {
			return fmt.Errorf("failed to validate jira link format")
		}

		if !matched {
			return fmt.Errorf("invalid jira link format. Expected format: https://jira.tools.sap/browse/<ISSUE-KEY>)")
		}
	}

	return nil
}

// used for both import and deploy
type DeliverOpRequest struct {
	OpIDs             []uint `json:"opIDs"`
	TargetTenant      uint   `json:"targetTenant"`
	DeliveryRequestID uint   `json:"deliveryRequestID"`
}

// General checks of dr before deliver(import/deploy) ops, like a hook
func (h *Handler) preDeliverCheck(req DeliverOpRequest) error {
	if len(req.OpIDs) == 0 || req.TargetTenant == 0 || req.DeliveryRequestID == 0 {
		return fmt.Errorf("missing opIDs, targetNode or deliveryRequestID")
	}
	var dr db.DeliveryRequest
	if err := h.db.First(&dr, req.DeliveryRequestID).Error; err != nil {
		return fmt.Errorf("delivery request id %d not found", req.DeliveryRequestID)
	}
	if dr.AggregateStatus == lifecycle.AggCanceled {
		return fmt.Errorf("delivery request #%d has been canceled, no operations allowed", req.DeliveryRequestID)
	}
	if dr.ApprovedBy == "" {
		return fmt.Errorf("delivery request not approved yet")
	}
	return nil
}

func (h *Handler) HandleImportOps(c *gin.Context) {
	var req DeliverOpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.preDeliverCheck(req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	user := service.UserID(c)
	success, err := h.svc.BatchImportTenantOps(req.DeliveryRequestID, req.OpIDs, req.TargetTenant, user)
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OKMsg(c, success, "Import triggered")
}

func (h *Handler) HandleDeployOps(c *gin.Context) {
	var req DeliverOpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.preDeliverCheck(req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	user := service.UserID(c)
	success, err := h.svc.BatchDeployTenantOps(req.DeliveryRequestID, req.OpIDs, req.TargetTenant, user)
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OKMsg(c, success, "Deploy triggered")
}

func (h *Handler) HandleSyncState(ctx *gin.Context) {
	drIDStr := ctx.Param("deliveryRequestId")
	if drIDStr == "" {
		Fail(ctx, http.StatusBadRequest, "missing query param: deliveryRequestId")
		return
	}
	user := service.UserID(ctx)
	drID, err := service.ToUint(drIDStr)
	if err != nil || drID <= 0 {
		Fail(ctx, http.StatusBadRequest, "invalid deliveryRequestId")
		return
	}
	if err := h.svc.SyncDeliveryStatus(drID, user); err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	OKMsg(ctx, nil, "sync finished")
}

type DeleteOpsRequest struct {
	OpIds             []uint `json:"opIds"`
	DeliveryRequestID uint   `json:"deliveryRequestID"`
}

func (h *Handler) HandleDeleteOps(c *gin.Context) {
	var req DeleteOpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.DeleteTenantOps(req.DeliveryRequestID, req.OpIds); err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, req.OpIds)
}

// update or insert ops request
type OpsRequest struct {
	Ops               []db.ArtifactTenantOperation `json:"ops"`
	DeliveryRequestID uint                         `json:"deliveryRequestID"`
}

func (h *Handler) HandleInsertOps(c *gin.Context) {
	var req OpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	user := service.UserID(c)
	if req.DeliveryRequestID == 0 {
		Fail(c, http.StatusBadRequest, "missing deliveryRequestID")
		return
	}
	var ops []db.ArtifactTenantOperation
	ops, err := h.svc.InsertTenantOps(c.Request.Context(), req.DeliveryRequestID, req.Ops, user)
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, ops)
}

func (h *Handler) HandleUpdateOps(c *gin.Context) {
	var req OpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	user := service.UserID(c)
	ops, err := h.svc.UpdateTenantOps(req.DeliveryRequestID, req.Ops, user)
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, ops)
}

func (h *Handler) HandleCheckTr(c *gin.Context) {
	var req struct {
		Op                db.ArtifactTenantOperation `json:"op"`
		DeliveryRequestID uint                       `json:"deliveryRequestID"`
	}
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var dr db.DeliveryRequest
	if err := h.db.First(&dr, req.DeliveryRequestID).Error; err != nil {
		Fail(c, http.StatusInternalServerError, fmt.Sprintf("failed to get delivery request id %d: %s", req.DeliveryRequestID, err))
		return
	}
	sourceTenantID := dr.SourceTenantID
	if req.Op.TenantID != sourceTenantID {
		Fail(c, http.StatusBadRequest, fmt.Sprintf("artifact tenant operation id %d has different source tenant id %d than delivery request source tenant id %d", req.Op.ID, req.Op.TenantID, sourceTenantID))
		return
	}
	var sourceTenant db.CpiTenant
	if err := h.db.First(&sourceTenant, sourceTenantID).Error; err != nil {
		Fail(c, http.StatusInternalServerError, fmt.Sprintf("failed to get source tenant id %d: %s", sourceTenantID, err))
		return
	}

	_, err := h.svc.TrExist(&req.Op, &sourceTenant)
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OKMsg(c, nil, "valid TR: "+req.Op.TransportRequestNumber)
}

// CancelDrRequest is the request body for canceling a delivery request
type CancelDrRequest struct {
	DeliveryRequestID uint   `json:"deliveryRequestID"`
	Reason            string `json:"reason"`
}

// HandleCancelDr cancels a delivery request with a reason.
func (h *Handler) HandleCancelDr(c *gin.Context) {
	var req CancelDrRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.DeliveryRequestID == 0 {
		Fail(c, http.StatusBadRequest, "missing deliveryRequestID")
		return
	}

	user := service.UserID(c)
	if err := h.svc.CancelDeliveryRequest(req.DeliveryRequestID, user, req.Reason); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}

	OKMsg(c, nil, "Delivery request canceled successfully")
}

// GenerateTR creates a TMS Transport Request for the given tenant via CAS.
//
// POST /api/v1/cpiTenant/:id/generateTR
//
// Request body:
//
//	{
//	  "deliveryRequestID": 42,
//	  "artifactOperationIDs": [1, 2, 3]
//	}
//
// Preconditions (hard-blocked, 400):
//   - tenant.LifecycleState == ready
//   - tenant.TmsNodeRegistrationStatus == ready
//
// DB loading, CAS catalog resolution, ContentResource grouping, and TR write-back
// are all handled by the service layer.
func (h *Handler) GenerateTR(ctx *gin.Context) {
	tenantID, ok := parseTenantID(ctx)
	if !ok {
		return
	}

	var body struct {
		DeliveryRequestID    uint   `json:"deliveryRequestID" binding:"required"`
		ArtifactOperationIDs []uint `json:"artifactOperationIDs" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "deliveryRequestID and artifactOperationIDs are required")
		return
	}
	if len(body.ArtifactOperationIDs) == 0 {
		Fail(ctx, 400, "artifactOperationIDs must not be empty")
		return
	}

	// TR generation can take up to several minutes (CAS export + poll loop).
	// Running it synchronously with the HTTP request context causes context.Canceled
	// when the client times out. Delegate to the same background function used by
	// InsertOps so TR results arrive via SSE instead of the HTTP response.
	go h.svc.GenerateTRsInBackground(body.DeliveryRequestID, tenantID, body.ArtifactOperationIDs)
	ctx.Status(202)
}
