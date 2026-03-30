package handler

import (
	"mmt-delivery/db"
	"mmt-delivery/service"
	"net/http"

	"github.com/gin-gonic/gin"
)

// add approvers to delivery request
func (h *Handler) HandleRequestApproval(ctx *gin.Context) {
	var approvalReq struct {
		Approvers []string `json:"approvers"`
		DrID      uint     `json:"deliveryRequestId"`
		Comment   string   `json:"comment"`
	}
	if err := ctx.ShouldBindJSON(&approvalReq); err != nil {
		Fail(ctx, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(approvalReq.Approvers) == 0 {
		Fail(ctx, http.StatusBadRequest, "no approvers provided")
		return
	}
	user := service.UserID(ctx)
	if err := h.svc.RequestApproval(approvalReq.DrID, user, approvalReq.Approvers, approvalReq.Comment); err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	OK(ctx, "approval request submitted")
}

func (h *Handler) HandleApproveDeliveryRequest(ctx *gin.Context) {
	var approvalReq struct {
		DrID    uint   `json:"deliveryRequestId"`
		Comment string `json:"comment"`
	}
	if err := ctx.ShouldBindJSON(&approvalReq); err != nil {
		Fail(ctx, http.StatusBadRequest, "invalid request body")
		return
	}
	user := service.UserID(ctx)
	var dr *db.DeliveryRequest
	var err error
	if dr, err = h.svc.Approve(approvalReq.DrID, user); err != nil {
		Fail(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	OK(ctx, dr)
}
