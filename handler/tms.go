package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetTmsNodesHandler(ctx *gin.Context) {
	c, err := h.svc.TmsSvc(ctx)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error resolving TMS client: %s", err))
		return
	}
	nodes, err := c.GetNodes(ctx)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error while retrieving tms Nodes: %s", err))
		return
	}
	OK(ctx, nodes)
}

func (h *Handler) GetRoutesHandler(ctx *gin.Context) {
	c, err := h.svc.TmsSvc(ctx)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error resolving TMS client: %s", err))
		return
	}
	routes, err := c.GetRoutes(ctx)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error while getting routes from TMS: %s", err))
		return
	}
	OK(ctx, routes)
}

func (h *Handler) GetTranportRequestsHandler(ctx *gin.Context) {
	trNode := ctx.Query("transportNode")
	nodeId, err := strconv.Atoi(trNode)
	if err != nil {
		Fail(ctx, http.StatusBadRequest, fmt.Sprintf("Invalid transport node id %s: %s", trNode, err))
		return
	}

	c, err := h.svc.TmsSvc(ctx)
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error resolving TMS client: %s", err))
		return
	}
	trs, err := c.GetNodeTransportRequests(ctx, uint(nodeId))
	if err != nil {
		Fail(ctx, http.StatusInternalServerError, fmt.Sprintf("Error while getting node transport requests: %s", err))
		return
	}
	OK(ctx, trs)
}
