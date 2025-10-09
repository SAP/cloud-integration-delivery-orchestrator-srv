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
	"mmt-delivery/pkg/tms"
)

func CreateDr(c *gin.Context) {
	var dr db.DeliveryRequest
	if err := c.ShouldBindJSON(&dr); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := User(c)
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

	// generate Transport routes
	if dr.SourceTenant.ID != 0 {
		tmsCli, error := tms.NewClient(c)
		if error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": error.Error()})
			return
		}
		transportRoutes, error := tmsCli.GetRoutes()
		if error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": error.Error()})
			return
		}
		transportNodes, error := tmsCli.GetNodes()
		if error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": error.Error()})
			return
		}

		dr.TargetRoutes, dr.TargetNodes = downstreamfromSource(dr.SourceTenant.TransportNode.ID, transportNodes, transportRoutes)
	}
	// check tr existence and origin. TODO: wrap a function in v1 to validate, check in parallel
	for i := range dr.ArtifactTenantOperations {
		// check tr Number existence and origin
		op := &dr.ArtifactTenantOperations[i]
		trNumber := op.TransportRequestNumber
		if trNumber == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("artifact %s has empty transport request number", op.ArtifactTechID)})
			return
		}
		tmsClient, err := tms.NewClient(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
			return
		}
		trV1, err := tmsClient.GetTransportRequest(trNumber) // v1 to check state
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Sprintf("error when getting transport request %s, the tr number may not exist, error message: %s", trNumber, err)})
			return
		}
		if trV1 == nil || trV1.ID == 0 || trV1.State != "RELEASED" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("artifact %s has invalid transport request number %s", op.ArtifactTechID, trNumber)})
			return
		}
		if trV1.Origin != dr.SourceTenant.TransportNode.Name { // can only be checked by origin node name, not id.
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("artifact %s has transport request number %s not from source tenant node %s", op.ArtifactTechID, trNumber, dr.SourceTenant.TransportNode.Name)})
			return
		}
		// check and save artifact, match techid and version.
		arti := &op.Artifact
		if db.Conn().FirstOrCreate(arti, &db.Artifact{TechID: arti.TechID, Version: arti.Version}).Error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Sprintf("error when saving artifact %s:%s", arti.TechID, arti.Version)})
			return
		}
		op.ArtifactID = arti.ID

		// update status of artifact tenant operation

	}
	dr.UpdatedBy = User(c)
	if err := db.Conn().Session(&gorm.Session{FullSaveAssociations: true}).Updates(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

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
	dr.UpdatedBy = User(c)
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
	user := User(c)
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

// downstreamfromSource returns all downstream routes and nodes reachable from a source node (BFS).
// It avoids cycles and duplicate routes/nodes.
// NOTE: the source node itself is NOT included in the returned targetNodes.
func downstreamfromSource(sourceNodeID uint, transportNodes []db.TransportNode, transportRoutes []db.TransportRoute) (targetRoutes []db.TransportRoute, targetNodes []db.TransportNode) {
	if sourceNodeID == 0 || len(transportRoutes) == 0 {
		return
	}

	// Build node lookup
	nodeMap := make(map[uint]db.TransportNode, len(transportNodes))
	for _, n := range transportNodes {
		nodeMap[n.ID] = n
	}

	// Adjacency: sourceNodeID -> routes originating there
	adj := make(map[uint][]db.TransportRoute)
	for _, r := range transportRoutes {
		adj[r.SourceNodeID] = append(adj[r.SourceNodeID], r)
	}

	if _, ok := nodeMap[sourceNodeID]; !ok {
		return
	}

	visitedNodes := make(map[uint]bool)
	visitedNodes[sourceNodeID] = true
	visitedRoutes := make(map[uint]bool)

	queue := []uint{sourceNodeID}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, r := range adj[curr] {
			// Skip duplicate route
			if visitedRoutes[r.ID] {
				continue
			}
			visitedRoutes[r.ID] = true
			targetRoutes = append(targetRoutes, r)

			// Enqueue target node if not seen
			if visitedNodes[r.TargetNodeID] {
				continue
			}
			if trNode, ok := nodeMap[r.TargetNodeID]; ok {
				targetNodes = append(targetNodes, trNode)
				visitedNodes[r.TargetNodeID] = true
				queue = append(queue, r.TargetNodeID)
			}
		}
	}

	return
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
