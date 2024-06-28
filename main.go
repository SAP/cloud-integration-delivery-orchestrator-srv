package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RootHandler(ctx *gin.Context) {
	ctx.HTML(http.StatusOK, "index/index.html", "maco-deploy")

}
func UserHandler(ctx *gin.Context) {
	ctx.HTML(http.StatusOK, "user/index.html", nil)

}
func main() {
	//engine := gin.New()
	router := gin.Default()
	router.LoadHTMLGlob("templates/**/*")
	router.Static("/static", "static")
	router.GET("/", RootHandler)
	router.GET("/user", UserHandler)
	router.Run(":9000")
}
