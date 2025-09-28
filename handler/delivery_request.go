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

		dr.TargetRoutes, dr.TargetNodes = DownstreamfromSource(dr.SourceTenant.TransportNode.ID, transportNodes, transportRoutes)
		if err := db.Conn().Save(&dr).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
			return
		}

	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": dr})
}

// downstream nodes and routes from a source node
func DownstreamfromSource(sourceNodeID uint, transportNodes []db.TransportNode, transportRoutes []db.TransportRoute) (targetRoutes []db.TransportRoute, targetNodes []db.TransportNode) {
	routesMap, nodesMap := make(map[uint]db.TransportRoute), make(map[uint]db.TransportNode)
	for _, route := range transportRoutes {
		routesMap[route.ID] = route
	}
	for _, node := range transportNodes {
		nodesMap[node.ID] = node
	}
	queue := make([]uint, 0)
	queue = append(queue, sourceNodeID)
	for len(queue) > 0 {
		length := len(queue)
		for range length {
			currentNodeID := queue[0] // pop
			queue = queue[1:]
			if route, exists := routesMap[currentNodeID]; exists {
				targetRoutes = append(targetRoutes, route)
				targetNodes = append(targetNodes, nodesMap[route.TargetNodeID])
				queue = append(queue, route.TargetNodeID)
			}

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
