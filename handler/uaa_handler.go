package handler

import (
	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
)

func (h *Handler) HandleUaaUserEmailSearch(c *gin.Context) {
	email := c.Param("email")
	origin := service.UaaOrigin(c)
	document, err := h.xsuaa.SearchByEmail(c, email, origin)
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": 200, "result": document})
}

func (h *Handler) HandleUaaUserIDSearch(c *gin.Context) {
	uid := c.Param("id")
	userInfo, err := h.xsuaa.UserInfo(c, uid)
	if err != nil {
		c.JSON(500, gin.H{"status": 500, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": 200, "result": userInfo})
}
