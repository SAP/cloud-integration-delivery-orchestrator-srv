package handler

import (
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/service"
	"net/http"

	"github.com/gin-gonic/gin"
)

func GetTransportGroups(ctx *gin.Context) {
	// search transport groups
	var transportGroups []db.TransportGroup
	if err := db.Conn().Find(&transportGroups).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to search transport groups: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"result": transportGroups})
}

func CreateTransportGroup(ctx *gin.Context) {
	// create transport group
	var transportGroup db.TransportGroup
	if err := ctx.BindJSON(&transportGroup); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Failed to bind transport group: " + err.Error()})
		return
	}
	user := service.User(ctx)
	transportGroup.CreatedBy, transportGroup.UpdatedBy = user, user
	if err := db.Conn().Save(&transportGroup).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create transport group: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"result": transportGroup})
}

func DeleteTransportGroup(ctx *gin.Context) {
	// delete transport group
	id := ctx.Query("id")
	if id == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Bad request. id is required"})
		return
	}
	if err := db.Conn().Delete(&db.TransportGroup{}, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to delete transport group: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"result": id, "msg": fmt.Sprintf("Transport group %s deleted", id)})
}
