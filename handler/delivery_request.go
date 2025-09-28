package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"mmt-delivery/db"
	"mmt-delivery/pkg/tms"
)

// Create or update a DeliveryRequest
func UpsertDeliveryRequest(c *gin.Context) {
	var dr db.DeliveryRequest
	if err := c.ShouldBindJSON(&dr); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": err.Error()})
		return
	}
	user := User(c)
	dr.UpdatedBy = user
	if dr.ID == 0 {
		dr.CreatedBy = user
	}
	if err := db.Conn().Save(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	// generate Transport routes
	if dr.SourceTenant.ID != 0 {
		tmsClient, error := tms.NewClient(c)
		if error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": error.Error()})
			return
		}
		transportRoutes, error := tmsClient.GetRoutes()
		if error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": error.Error()})
			return
		}
		transportNodes, error := tmsClient.GetNodes()
		if error != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": error.Error()})
			return
		}

		dr.TargetRoutes, dr.TargetNodes = nodesAndRoutesFromSourceTenant(dr.SourceTenant.TransportNode.ID, transportNodes, transportRoutes)
		if err := db.Conn().Save(&dr).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
			return
		}

	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

func nodesAndRoutesFromSourceTenant(sourceNodeID uint, transportNodes []db.TransportNode, transportRoutes []db.TransportRoute) (targetRoutes []db.TransportRoute, targetNodes []db.TransportNode) {
	sourceNodeIDs := make(map[uint]bool)
	sourceNodeIDs[sourceNodeID] = true

	for _, r := range transportRoutes {
		if sourceNodeIDs[r.SourceNodeID] {
			targetRoutes = append(targetRoutes, r)
			targetNodes = append(targetNodes, transportNodes[r.TargetNodeID])
			sourceNodeIDs[r.TargetNodeID] = true
		}
	}
	return
}

// List all DeliveryRequests
func GetDeliveryRequests(c *gin.Context) {
	var list []db.DeliveryRequest
	if err := db.Conn().
		Preload("SourceTenant").
		Preload("DeliveryRule").
		Find(&list).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": list})
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
		First(&dr, id).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// Delete DeliveryRequest by id
func DeleteDeliveryRequest(c *gin.Context) {
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
