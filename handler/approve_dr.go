package handler

import (
	"mmt-delivery/service"
	"net/http"

	"github.com/gin-gonic/gin"
)

// add approvers to delivery request
func HandleRequestApproval(ctx *gin.Context) {
	var approvalReq struct {
		Approvers []string `json:"approvers"`
		DrID      uint     `json:"deliveryRequestId"`
		Comment   string   `json:"comment"`
	}
	if err := ctx.ShouldBindJSON(&approvalReq); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid request body"})
		return
	}
	if len(approvalReq.Approvers) == 0 {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "no approvers provided"})
		return
	}
	user := service.UaaClaim(ctx)
	if err := service.RequestApproval(approvalReq.DrID, user.Subject, approvalReq.Approvers, approvalReq.Comment); err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": "approval request submitted"})
}

func HandleApproveDeliveryRequest(ctx *gin.Context) {
	var approvalReq struct {
		DrID uint `json:"deliveryRequestId"`
		Comment string `json:"comment"`
	}
	if err := ctx.ShouldBindJSON(&approvalReq); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"status": "fail", "code": 400, "error": "invalid request body"})
		return
	}
	user := service.UaaClaim(ctx)
	if err := service.Approve(approvalReq.DrID, user.UserID); err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": "delivery request approved"})
}
