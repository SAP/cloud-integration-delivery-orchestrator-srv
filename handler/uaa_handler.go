package handler

import (
	"mmt-delivery/pkg/xsuaa"
	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
)

func HandleUaaUserEmailSearch(c *gin.Context) {
	email := c.Param("email")
	uaaClient, err := xsuaa.NewClient(c.Copy())
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	origin := service.UaaOrigin(c)
	document, err := uaaClient.SearchByEmail(email, origin)
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": 200, "result": document})
}

func HandleUaaUserIDSearch(c *gin.Context) {
	uid := c.Param("id")
	uaaClient, err := xsuaa.NewClient(c.Copy())
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	userInfo, err := uaaClient.UserInfo(uid)
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": 200, "result": userInfo})
}
