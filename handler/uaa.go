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
		Fail(c, 500, err.Error())
		return
	}
	OK(c, document)
}

func (h *Handler) HandleUaaUserIDSearch(c *gin.Context) {
	uid := c.Param("id")
	userInfo, err := h.xsuaa.UserInfo(c, uid)
	if err != nil {
		Fail(c, 500, err.Error())
		return
	}
	OK(c, userInfo)
}
