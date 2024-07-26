package main

import (
	"net/http"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/handler"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
)

func RootHandler(ctx *gin.Context) {
	ctx.HTML(http.StatusOK, "index/index.html", "maco-deploy")

}

func main() {
	//engine := gin.New()
	router := gin.New()
	var logger = log.NewLogger()
	router.Use(ginzap.Ginzap(logger, time.RFC3339, true))
	router.Use(ginzap.RecoveryWithZap(logger, true))
	router.LoadHTMLGlob("templates/**/*")
	router.Static("/static", "static")
	router.GET("/", RootHandler)
	router.GET("/api/v1/users", handler.GetUserInfoHandler)
	router.GET("/api/v1/user/*username", handler.GetUsersHandler)
	router.POST("/api/v1/user", handler.CreateUsersHandler)
	router.GET("/api/v1/config", handler.GetConfigs)
	router.DELETE("/api/v1/config", handler.DeleteConfig)
	router.POST("/api/v1/config", handler.CreateConfig)
	router.Run(":9000")

}
