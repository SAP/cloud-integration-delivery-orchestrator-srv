package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/handler"
)

func RootHandler(ctx *gin.Context) {
	ctx.HTML(http.StatusOK, "index/index.html", "maco-deploy")

}

func main() {
	//engine := gin.New()
	router := gin.Default()
	router.LoadHTMLGlob("templates/**/*")
	router.Static("/static", "static")

	router.GET("/", RootHandler)
	router.GET("/api/v1/users", handler.GetUserInfoHandler)
	router.GET("/api/v1/user/*username", handler.GetUsersHandler)
	router.POST("/api/v1/user", handler.CreateUsersHandler)
	router.GET("/api/v1/cpiconfigs", handler.GetCpiConfigs)
	router.GET("/api/v1/cpiconfig", handler.GetCpiConfig)
	router.POST("/api/v1/cpiconfig", handler.CreateCpiConfig)
	router.Run(":9000")

}
