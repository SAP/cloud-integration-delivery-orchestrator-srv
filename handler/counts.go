package handler

import (
	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"

	"github.com/gin-gonic/gin"
)

type StatusCount struct {
	Total        int64
	StatusCounts map[string]uint
}

func DeliveryRequestCounts(ctx *gin.Context) {
	var res StatusCount
	if err := db.Conn().Model(&db.DeliveryRequest{}).Count(&res.Total).Error; err != nil {
		ctx.JSON(500, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	var counts []struct {
		AggregateStatus lifecycle.AggregateStatus
		Count           uint
	}
	if err := db.Conn().Model(&db.DeliveryRequest{}).Select("aggregate_status, count(*)").Group("aggregate_status").Scan(&counts).Error; err != nil {
		ctx.JSON(500, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	res.StatusCounts = make(map[string]uint)
	for _, c := range counts {
		res.StatusCounts[string(c.AggregateStatus)] = c.Count
	}
	ctx.JSON(200, gin.H{"status": "success", "code": 200, "result": res})
}

func CpiTenantCounts(ctx *gin.Context) {
	var res StatusCount
	if err := db.Conn().Model(&db.CpiTenant{}).Count(&res.Total).Error; err != nil {
		ctx.JSON(500, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(200, gin.H{"status": "success", "code": 200, "result": res})
}

func DeliveryRuleCounts(ctx *gin.Context) {
	var res StatusCount
	if err := db.Conn().Model(&db.DeliveryRule{}).Count(&res.Total).Error; err != nil {
		ctx.JSON(500, gin.H{"status": "fail", "code": 500, "error": err.Error()})
		return
	}
	ctx.JSON(200, gin.H{"status": "success", "code": 200, "result": res})
}
