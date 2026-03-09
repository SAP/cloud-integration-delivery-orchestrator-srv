package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"mmt-delivery/db"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetTmsNodesHandler(ctx *gin.Context) {
	tmsNodes, error := h.tms.GetNodes(ctx)
	if error != nil {
		errorMsg := fmt.Sprintf("Error while retrieving tms Nodes: %s", error)
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "result": errorMsg})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": tmsNodes})
}

func (h *Handler) GetRoutesHandler(ctx *gin.Context) {
	routes, err := h.tms.GetRoutes(ctx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": fmt.Sprintf("Error while get routes from tms: %s", err)})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": routes})
}

func (h *Handler) GetTranportRequestsHandler(ctx *gin.Context) {
	trNode := ctx.Query("transportNode")
	nodeId, error := strconv.Atoi(trNode)
	if error != nil {
		errorMsg := fmt.Sprintf("Invalid transport node id %s: %s", trNode, error)
		h.logger.Error(errorMsg)
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 500, "result": errorMsg})
		return
	}

	trs, error := h.tms.GetNodeTransportRequests(ctx, uint(nodeId))
	if error != nil {
		errorMsg := fmt.Sprintf("Error while get node trs: %s", error)
		h.logger.Error(errorMsg)
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "result": errorMsg})
		return
	}
	trResp := make([]db.TransportRequest, len(trs))
	for i := range trs {
		trResp[i] = db.TransportRequest{
			ID:          trs[i].ID,
			Description: trs[i].Description,
			Status:      trs[i].Status,
		}
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": trs})
}

// TODO: monitoring and logging if transport request is not
// successful: https://api.sap.com/api/TMS_v2/resource/
