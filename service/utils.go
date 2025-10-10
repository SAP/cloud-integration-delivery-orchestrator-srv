package service

import (
	"strconv"

	"github.com/gin-gonic/gin"
)



func User(ctx *gin.Context) string {
	email, _ := ctx.Get("user")
	return email.(string)
}

func ToUint(s string) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	return uint(v), err
}
