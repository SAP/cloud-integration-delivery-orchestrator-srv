package handler

import (
	"mmt-delivery/pkg/xsuaa"

	"github.com/gin-gonic/gin"
)

func HandleUaaUserSearch(c *gin.Context) {
	email := c.Param("email")
	uaaClient, err := xsuaa.NewClient(c.Copy())
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	document, err := uaaClient.SearchUserByEmail(email)
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": 200, "result": document})
}
