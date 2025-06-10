package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"mmt-delivery/db"
	"mmt-delivery/pkg/tms"
)

func GetTmsNodesHandler(ctx *gin.Context) {

	tmsClient, error := tms.NewClient(ctx)
	if error != nil {
		return
	}
	tmsNodes, error := tmsClient.GetNodes()
	if error != nil {
		errorMsg := fmt.Sprintf("Error while retrieving tms Nodes: %s", error)
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 500, "result": errorMsg})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": tmsNodes})
}

func GetTranportRequestsHandler(ctx *gin.Context) {
	trNode := ctx.Query("transportNode")
	nodeId, error := strconv.Atoi(trNode)
	if error != nil {
		errorMsg := fmt.Sprintf("Invalid transport node id %s: %s", trNode, error)
		logger.Error(errorMsg)
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 500, "result": errorMsg})
		return
	}

	tmsClient, error := tms.NewClient(ctx)
	if error != nil {
		logger.Error(error)
		return
	}
	trs, error := tmsClient.GetNodeTransportRequests(nodeId)
	trResp := make([]db.TransportRequest, len(trs))
	for i := range trs {
		trResp[i] = db.TransportRequest{
			ID:          trs[i].ID,
			Description: trs[i].Description,
			Status:      trs[i].Status,
		}
	}
	if error != nil {
		errorMsg := fmt.Sprintf("Error while get node trs: %s", error)
		logger.Error(errorMsg)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": trs})
}

// TODO: monitoring and logging if transport request is not
// successful: https://api.sap.com/api/TMS_v2/resource/
