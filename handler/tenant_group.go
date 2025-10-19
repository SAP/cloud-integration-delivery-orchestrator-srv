package handler

import (
	"errors"
	"mmt-delivery/db"
	"mmt-delivery/service"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var repo = service.NewTenantGroupRepo()

func GetTenantGroupByID(c *gin.Context) {
	groupIDStr := c.Param("id")
	groupID, err := service.ToUint(groupIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "Invalid tenant group ID: " + err.Error()})
		return
	}
	g, err := repo.GetByID(c, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to get tenant group: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": g})
}

func CreateTenantGroup(c *gin.Context) {
	var g db.TenantGroup
	if err := c.BindJSON(&g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "Failed to bind tenant group: " + err.Error()})
		return
	}
	if err := repo.Create(c, &g); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create tenant group: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": g})
}

func UpdateTenantGroup(c *gin.Context) {
	var g db.TenantGroup
	if err := c.BindJSON(&g); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "Failed to bind tenant group: " + err.Error()})
		return
	}
	if err := repo.Update(c, &g); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"msg": "Tenant group not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to update tenant group: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": g})
}

func ListTenantGroups(c *gin.Context) {
	groups, err := repo.List(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to list tenant groups: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": groups})
}

func DeleteTenantGroup(c *gin.Context) {
	groupIDStr := c.Param("id")
	groupID, err := service.ToUint(groupIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "Invalid tenant group ID: " + err.Error()})
		return
	}
	if err := repo.Delete(c, groupID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to delete tenant group: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "code": 200, "result": groupID})
}
