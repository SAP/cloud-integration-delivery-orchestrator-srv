package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/service"
)

func CreateDr(c *gin.Context) {
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

	if !checkJIRA(dr.JiraLink) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid jira link"})
		return
	}
	var rule db.DeliveryRule
	if rule, err = service.GetDeliveryRule(dr.DeliveryRule.ID); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Errorf("failed to generate target routes and nodes: %s", err)})
		return
	}
	dr.SourceTenantID = rule.SourceTenantID
	dr.DeliveryRuleID = rule.ID

	dr.AggregateStatus = lifecycle.AggPending

	user := service.UserID(c)
	dr.CreatedBy, dr.UpdatedBy = user, user

	if err := db.Conn().Create(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// update DeliveryRequest, not including ops
func UpdateDr(c *gin.Context) {
	var dr db.DeliveryRequest
	if err := c.ShouldBindJSON(&dr); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	var existing db.DeliveryRequest
	if err := db.Conn().First(&existing, dr.ID).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"status": "fail", "code": 404, "error": fmt.Sprintf("delivery request id %d not found", dr.ID)})
		return
	}
	if existing.AggregateStatus != lifecycle.AggPending {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "only pending delivery request can be updated"})
		return
	}
	user, now := service.UserID(c), time.Now()
	// check and update JIRA
	if existing.JiraLink != dr.JiraLink {
		if !checkJIRA(dr.JiraLink) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid jira link"})
			return
		}
		existing.JiraLink = dr.JiraLink
		existing.UpdatedBy, existing.UpdatedAt = user, now
	}
	if existing.DeliveryRuleID != dr.DeliveryRule.ID {
		// TODO: check artifact ops in this dr. prevent changing rule if ops has different source tenant id
		existing.DeliveryRuleID = dr.DeliveryRule.ID
		dr.UpdatedBy, dr.UpdatedAt = user, now
	}
	if err := db.Conn().Updates(&existing).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// List all DeliveryRequests
func GetAllDr(c *gin.Context) {
	var drList []db.DeliveryRequest
	if err := db.Conn().
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Find(&drList).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": drList})
}

// Get a single DeliveryRequest by id
func GetDeliveryRequest(c *gin.Context) {
	raw := c.Param("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}
	var dr db.DeliveryRequest
	if err := db.Conn().
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Preload("ArtifactTenantOperations.Artifact").
		Preload("ArtifactTenantOperations.Tenant").
		First(&dr, id).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// DeleteDr DeliveryRequest by id
func DeleteDr(c *gin.Context) {
	raw := c.Param("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}
	if err := db.Conn().Delete(&db.DeliveryRequest{}, id).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": id})
}

// TODO: finish this
func checkJIRA(jira string) bool {
	if jira == "" {
		return true
	}
	return true
}

// used for both import and deploy
type DeliverOpRequest struct {
	OpIDs        []uint `json:"opIDs"`
	TargetTenant uint   `json:"targetTenant"`
}

func HandleImportOps(c *gin.Context) {
	var req DeliverOpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if len(req.OpIDs) == 0 || req.TargetTenant == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing opIDs or targetNode"})
		return
	}
	user := service.UserID(c)
	success, err := service.BatchImportTenantOps(req.OpIDs, req.TargetTenant, user)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": success})
}

func HandleDeployOps(c *gin.Context) {
	var req DeliverOpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	if len(req.OpIDs) == 0 || req.TargetTenant == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing opIDs or targetNode"})
		return
	}
	user := service.UserID(c)
	success, err := service.BatchDeployTenantOps(req.OpIDs, req.TargetTenant, user)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": success})
}

func HandleSyncState(ctx *gin.Context) {
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
	if err := service.SyncImportState(drID, user); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": err.Error()})
		return
	}
	if err := service.SyncDeployState(drID, user); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": "sync finished"})
}

type DeleteOpsRequest struct {
	OpIds []uint `json:"opIds"`
}

func HandleDeleteOps(c *gin.Context) {
	var req DeleteOpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
	}
	if err := service.DeleteTenantOps(req.OpIds); err != nil {
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

func HandleInsertOps(c *gin.Context) {
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
	if err := service.InsertTenantOps(req.DeliveryRequestID, req.Ops, user); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": req.Ops})

}

func HandleUpdateOps(c *gin.Context) {
	var req OpsRequest
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := service.UserID(c)
	if err := service.UpdateTenantOps(req.DeliveryRequestID, req.Ops, user); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": req.Ops})
}
