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
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if dr.DeliveryRule.ID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing delivery rule"})
		return
	}
	if dr.Name == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing name"})
		return
	}

	if err := checkJIRA(dr.JiraLink, dr.DeliveryRule); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	var rule db.DeliveryRule
	if rule, err = h.svc.GetDeliveryRuleWithAcc(dr.DeliveryRule.ID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Errorf("failed to generate target routes and nodes: %s", err)})
		return
	}
	dr.SourceTenantID = rule.SourceTenantID
	dr.DeliveryRuleID = rule.ID

	dr.AggregateStatus = lifecycle.AggPending

	user := service.UserID(c)
	dr.CreatedBy, dr.UpdatedBy = user, user

	if err := h.db.Create(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// update DeliveryRequest, not including ops
func (h *Handler) UpdateDr(c *gin.Context) {
	var dr db.DeliveryRequest
	if err := c.ShouldBindJSON(&dr); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	var existing db.DeliveryRequest
	if err := h.db.First(&existing, dr.ID).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"status": "fail", "code": 404, "error": fmt.Sprintf("delivery request id %d not found", dr.ID)})
		return
	}
	if existing.AggregateStatus != lifecycle.AggPending && existing.AggregateStatus != lifecycle.AggWaitingApprove {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "only pending or waiting approval delivery request can be updated"})
		return
	}
	rule, err := h.svc.GetDeliveryRuleWithAcc(dr.DeliveryRule.ID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	user := service.UserID(c)
	// check and update JIRA
	if existing.JiraLink != dr.JiraLink {
		if err := checkJIRA(dr.JiraLink, rule); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
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
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// List all DeliveryRequests
func (h *Handler) GetAllDr(c *gin.Context) {
	var drList []db.DeliveryRequest
	if err := h.db.
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Find(&drList).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": drList})
}

// Get a single DeliveryRequest by id
func (h *Handler) GetDeliveryRequest(c *gin.Context) {
	raw := c.Param("id")
	drID, err := strconv.Atoi(raw)
	if err != nil || drID <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}
	dr, err := h.svc.QueryDrWithAssociations(uint(drID))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
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
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}
	if err := h.db.Delete(&db.DeliveryRequest{}, id).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": id})
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
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if err := h.preDeliverCheck(req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := service.UserID(c)
	success, err := h.svc.BatchImportTenantOps(req.DeliveryRequestID, req.OpIDs, req.TargetTenant, user)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": success, "msg": "Import triggered"})
}

func (h *Handler) HandleDeployOps(c *gin.Context) {
	var req DeliverOpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if err := h.preDeliverCheck(req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := service.UserID(c)
	success, err := h.svc.BatchDeployTenantOps(req.DeliveryRequestID, req.OpIDs, req.TargetTenant, user)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": success, "msg": "Deploy triggered"})
}

func (h *Handler) HandleSyncState(ctx *gin.Context) {
	drIDStr := ctx.Param("deliveryRequestId")
	if drIDStr == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 400, "error": "missing query param: deliveryRequestId"})
		return
	}
	user := service.UserID(ctx)
	drID, err := service.ToUint(drIDStr)
	if err != nil || drID <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 400, "error": "invalid deliveryRequestId"})
		return
	}
	if err := h.svc.SyncDeliveryStatus(drID, user); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": 200, "msg": "sync finished"})
}

type DeleteOpsRequest struct {
	OpIds []uint `json:"opIds"`
}

func (h *Handler) HandleDeleteOps(c *gin.Context) {
	var req DeleteOpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
	}
	if err := h.svc.DeleteTenantOps(req.OpIds); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": req.OpIds})

}

// update or insert ops request
type OpsRequest struct {
	Ops               []db.ArtifactTenantOperation `json:"ops"`
	DeliveryRequestID uint                         `json:"deliveryRequestID"`
}

func (h *Handler) HandleInsertOps(c *gin.Context) {
	var req OpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := service.UserID(c)
	if req.DeliveryRequestID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing deliveryRequestID"})
		return
	}
	var ops []db.ArtifactTenantOperation
	ops, err := h.svc.InsertTenantOps(req.DeliveryRequestID, req.Ops, user)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": ops})

}

func (h *Handler) HandleUpdateOps(c *gin.Context) {
	var req OpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := service.UserID(c)
	ops, err := h.svc.UpdateTenantOps(req.DeliveryRequestID, req.Ops, user)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": ops})
}

func (h *Handler) HandleCheckTr(c *gin.Context) {
	var req struct {
		Op                db.ArtifactTenantOperation `json:"op"`
		DeliveryRequestID uint                       `json:"deliveryRequestID"`
	}
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	var dr db.DeliveryRequest
	if err := h.db.First(&dr, req.DeliveryRequestID).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Sprintf("failed to get delivery request id %d: %s", req.DeliveryRequestID, err)})
		return
	}
	sourceTenantID := dr.SourceTenantID
	if req.Op.TenantID != sourceTenantID {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("artifact tenant operation id %d has different source tenant id %d than delivery request source tenant id %d", req.Op.ID, req.Op.TenantID, sourceTenantID)})
		return
	}
	var sourceTenant db.CpiTenant
	if err := h.db.First(&sourceTenant, sourceTenantID).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Sprintf("failed to get source tenant id %d: %s", sourceTenantID, err)})
		return
	}

	_, err := h.svc.TrExist(&req.Op, &sourceTenant)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "msg": "valid TR: " + req.Op.TransportRequestNumber})
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
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if req.DeliveryRequestID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing deliveryRequestID"})
		return
	}

	user := service.UserID(c)
	if err := h.svc.CancelDeliveryRequest(req.DeliveryRequestID, user, req.Reason); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "message": "Delivery request canceled successfully"})
}
