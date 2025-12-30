package service

import (
	"mmt-delivery/db"
	"strconv"

	"github.com/gin-gonic/gin"
)

func UserEmail(ctx *gin.Context) string {
	email, _ := ctx.Get("user_name")
	return email.(string)
}
func UserID(ctx *gin.Context) string {
	claim, _ := ctx.Get("uaa_claim")
	uid := claim.(db.UaaClaims).Subject
	return uid
}

func UaaOrigin(ctx *gin.Context) string {
	origin, _ := ctx.Get("origin")
	return origin.(string)
}

func UaaClaim(ctx *gin.Context) db.UaaClaims {
	claim, _ := ctx.Get("uaa_claim")
	return claim.(db.UaaClaims)
}

func Scope(ctx *gin.Context) []string {
	scope, _ := ctx.Get("scope")
	return scope.([]string)
}
func ToUint(s string) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	return uint(v), err
}
