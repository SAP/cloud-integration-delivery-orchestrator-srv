package handler

import (
	"mmt-delivery/db"
	"mmt-delivery/service"
	"strings"

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

// GetCurrentUserScopes returns the current user's scopes from the JWT token,
// with the $XSAPPNAME prefix stripped for readability.
func (h *Handler) GetCurrentUserScopes(c *gin.Context) {
	claims, exists := c.Get("uaa_claim")
	if !exists {
		Fail(c, 403, "no authentication claims found")
		return
	}
	uaaClaims := claims.(db.UaaClaims)

	// Strip xsappname prefix (e.g. "cpi-delivery!t14446.DeliveryRequest.Read" → "DeliveryRequest.Read")
	scopes := make([]string, 0, len(uaaClaims.Scope))
	for _, s := range uaaClaims.Scope {
		if idx := strings.Index(s, "."); idx != -1 {
			scopes = append(scopes, s[idx+1:])
		} else {
			scopes = append(scopes, s)
		}
	}

	OK(c, gin.H{
		"scopes": scopes,
		"origin": uaaClaims.Origin,
	})
}
