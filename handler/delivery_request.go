package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/service"
)

func CreateDr(c *gin.Context) {
	var dr db.DeliveryRequest
	if err := c.ShouldBindJSON(&dr); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := service.User(c)
	dr.CreatedBy, dr.UpdatedBy = user, user

	if !checkJIRA(dr.JiraLink) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid jira link"})
	}

	if dr.DeliveryRule.ID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing delivery rule"})
	}

	dr.AggregateStatus = lifecycle.AggPending

	if err := db.Conn().Create(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// update DeliveryRequest
func UpdateDr(c *gin.Context) {
	var dr db.DeliveryRequest
	if err := c.ShouldBindJSON(&dr); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	// check existence
	var count int64
	db.Conn().Model(&db.DeliveryRequest{}).Where("id = ?", dr.ID).Count(&count)
	if count == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("delivery request id %d not found", dr.ID)})
		return
	}
	if dr.SourceTenant.ID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "missing source tenant"})
		return
	}
	targetRoutes, targetNodes, err := service.GenRoute(*dr.SourceTenant)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Errorf("failed to generate target routes and nodes: %s", err)})
		return
	}
	dr.TargetNodes, dr.TargetRoutes = targetNodes, targetRoutes

	// check tr existence and origin. TODO: wrap a function in v1 to validate, check in parallel
	if _, err := service.TrExist(dr.ArtifactTenantOperations, dr.SourceTenant); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	// load artifact info, set ArtifactID in ops
	for i := range dr.ArtifactTenantOperations {
		op := &dr.ArtifactTenantOperations[i]
		aID, err := service.LoadArtifact(*op)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Errorf("failed to load artifact for operation %d: %s", op.ID, err)})
			return
		}
		op.ArtifactID = aID
		op.Artifact.ID = aID  // to avoid unique constraint, since FullSaveAssociations in main table
	}

	dr.UpdatedBy = service.User(c)
	if err := db.Conn().Session(&gorm.Session{FullSaveAssociations: true}).Updates(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	go service.SyncImportState(dr.ID)

	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// Ask for approval of a pending delivery request
func ApplyApprove(c *gin.Context) {
	idRaw := c.Param("id")
	id, err := strconv.Atoi(idRaw)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}

	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, id).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"status": "fail", "code": 404, "error": fmt.Sprintf("delivery request id %d not found", id)})
		return
	}

	if dr.ApprovedAt != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "delivery request already approved"})
		return
	}
	if dr.AggregateStatus != lifecycle.AggPending {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "only pending delivery request can be asked for approval"})
		return
	}

	dr.AggregateStatus = lifecycle.AggWaitingApprove
	dr.UpdatedBy = service.User(c)
	if err := db.Conn().Save(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	// TODO: send email to approver, update JIRA

	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

func Approve(c *gin.Context) {
	idRaw := c.Param("id")
	id, err := strconv.Atoi(idRaw)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid id"})
		return
	}

	var dr db.DeliveryRequest
	if err := db.Conn().First(&dr, id).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"status": "fail", "code": 404, "error": fmt.Sprintf("delivery request id %d not found", id)})
		return
	}

	if dr.AggregateStatus != lifecycle.AggWaitingApprove {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "only delivery request in waiting approval status can be approved"})
		return
	}

	if dr.ApprovedAt != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "delivery request already approved"})
		return
	}
	user := service.User(c)
	if user == dr.CreatedBy {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "creator cannot approve own request"})
		return
	}

	dr.ApprovedBy, dr.UpdatedBy = user, user
	now := time.Now()
	dr.ApprovedAt = &now

	dr.AggregateStatus = lifecycle.AggAwaitingImport

	if err := db.Conn().Save(&dr).Error; err != nil {
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

func checkJIRA(jira string) bool {
	if jira == "" {
		return true
	}
	return true
}
