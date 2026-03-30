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
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error while retrieving tms Nodes: %s", error))
		return
	}
	OK(ctx, tmsNodes)
}

func (h *Handler) GetRoutesHandler(ctx *gin.Context) {
	routes, err := h.tms.GetRoutes(ctx)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error while get routes from tms: %s", err))
		return
	}
	OK(ctx, routes)
}

func (h *Handler) GetTranportRequestsHandler(ctx *gin.Context) {
	trNode := ctx.Query("transportNode")
	nodeId, error := strconv.Atoi(trNode)
	if error != nil {
		errorMsg := fmt.Sprintf("Invalid transport node id %s: %s", trNode, error)
		h.logger.Error(errorMsg)
		Fail(ctx, http.StatusBadRequest, errorMsg)
		return
	}

	trs, error := h.tms.GetNodeTransportRequests(ctx, uint(nodeId))
	if error != nil {
		errorMsg := fmt.Sprintf("Error while get node trs: %s", error)
		h.logger.Error(errorMsg)
		Fail(ctx, http.StatusInternalServerError, errorMsg)
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
	OK(ctx, trs)
}

// TODO: monitoring and logging if transport request is not
// successful: https://api.sap.com/api/TMS_v2/resource/
