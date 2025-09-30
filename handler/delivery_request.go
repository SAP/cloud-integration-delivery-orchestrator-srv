package handler

import (
	"fmt"
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
	// TODO: not okay to save herer, should check first
	if err := db.Conn().Save(&dr).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}

	// TODO: do routes generate, check tr in parallel
	// generate Transport routes
	if dr.SourceTenantID != nil {
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

		dr.TargetRoutes, dr.TargetNodes = downstreamfromSource(dr.SourceTenant.TransportNode.ID, transportNodes, transportRoutes)
		if err := db.Conn().Save(&dr).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
			return
		}
	}
	// check TransportRequestNumbers
	// TODO: wrap a function in v1 to validate
	for _, a := range dr.Artifacts {
		trNumber := a.TransportRequestNumber
		if trNumber == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("artifact %s has empty transport request number", a.Name)})
			return
		}
		tmsClient, err := tms.NewClient(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
			return
		}
		trV1, err := tmsClient.GetTransportRequest(a.TransportRequestNumber) // v1 to check state
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": fmt.Sprintf("error when getting transport request %s, the tr number may not exist, error message: %s", trNumber, err)})
			return
		}
		if trV1 == nil || trV1.ID == 0 || trV1.State != "RELEASED" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("artifact %s has invalid transport request number %s", a.Name, trNumber)})
			return
		}
		if trV1.Origin != dr.SourceTenant.TransportNode.Name { // check origin node
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": fmt.Sprintf("artifact %s has transport request number %s not from source tenant node %s", a.Name, trNumber, dr.SourceTenant.TransportNode.Name)})
			return
		}
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
