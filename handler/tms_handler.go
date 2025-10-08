package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
	"mmt-delivery/pkg/tms"

	"github.com/gin-gonic/gin"
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

// sync import status of all artifacts under a delivery request in TMS node
func SyncImportState(ctx *gin.Context) {
	var artifactOps []db.ArtifactTenantOperation
	// Search ArtifactTenantOperation by DeliveryRequestID
	deliveryRequestIDStr := ctx.Query("deliveryRequestId")
	if deliveryRequestIDStr == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 400, "error": "missing query param: deliveryRequestId"})
		return
	}

	deliveryRequestID, err := strconv.Atoi(deliveryRequestIDStr)
	if err != nil || deliveryRequestID <= 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 400, "error": "invalid deliveryRequestId"})
		return
	}

	// Adjust the DB accessor (db.DB / db.GetDB()) to match your project setup
	if err := db.Conn().Where("delivery_request_id = ?", deliveryRequestID).Find(&artifactOps).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": fmt.Sprintf("db query failed: %s", err)})
		return
	}
	if len(artifactOps) == 0 {
		ctx.JSON(http.StatusNotFound, gin.H{"status": 404, "result": []db.ArtifactTenantOperation{}})
		return
	}
	trStatus := make(map[string]map[uint]tms.TrNodeStatus) // tr number status in all nodes. key: artifactID, value: map[nodeID]status

	tmsClient, err := tms.NewClient(ctx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": fmt.Sprintf("error creating tms client: %s", err)})
		return
	}
	for _, a := range artifactOps {
		trNumber := a.TransportRequestNumber
		if trNumber == "" {
			continue
		}
		if _, ok := trStatus[trNumber]; ok {
			continue
		}
		// UpdateArtifactNodeStatus will call GetTransportRequest internally
		ns, err := tmsClient.SyncTrNodeStatus(trNumber)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": fmt.Sprintf("error when getting transport request %s, the tr number may not exist, error message: %s", trNumber, err)})
			return
		}
		trStatus[trNumber] = ns
	}
	// update import state of each artifact tenant operation
	for i, a := range artifactOps {
		trNumber := a.TransportRequestNumber
		if trNumber == "" {
			continue
		}
		artifactNodeState := trStatus[trNumber][a.Tenant.TransportNode.ID].Status
		artifactOps[i].ImportState = lifecycle.DeriveImport(artifactNodeState)
		if db.Conn().Model(&artifactOps[i]).Updates(&db.ArtifactTenantOperation{ImportState: artifactOps[i].ImportState}).Error != nil {
			// TODO: use condition to track import status
			ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": fmt.Sprintf("error when updating artifact %s import state to %s", a.ArtifactTechID, artifactOps[i].ImportState)})
			return
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": artifactOps})
}

func GetRoutesHandler(ctx *gin.Context) {
	tmsClient, error := tms.NewClient(ctx)
	if error != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": fmt.Sprintf("Error while creating tms client: %s", error)})
		return
	}
	routes, err := tmsClient.GetRoutes()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": fmt.Sprintf("Error while get routes from tms: %s", err)})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": routes})
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
	trs, error := tmsClient.GetNodeTransportRequests(uint(nodeId))
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
