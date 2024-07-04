package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

const (
	dbDriver = "postgres"
	dbSource = "postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable"
)

func RootHandler(ctx *gin.Context) {
	ctx.HTML(http.StatusOK, "index/index.html", "maco-deploy")

}

func GetUserInfoHandler(ctx *gin.Context) {
	name := ctx.Param("username")
	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, dbSource)
	if errDBconn != nil {
		log.Fatal("error connecting to db,", errDBconn)
	}
	query := db.New(dbConn)
	user, errUer := query.GetUser(context, name)
	if errUer != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "user not found"})

	} else {
		ctx.JSON(http.StatusOK, gin.H{"code": 200, "result": user})
	}

}

func GetUsersHandler(ctx *gin.Context) {
	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, dbSource)
	if errDBconn != nil {
		log.Fatal("error connecting to db,", errDBconn)
	}
	query := db.New(dbConn)
	users, errUer := query.GetUsers(context)
	if errUer != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "user not found"})
	} else {
		ctx.JSON(http.StatusOK, gin.H{"code": 200, "result": users})
	}

}

func main() {
	//engine := gin.New()
	router := gin.Default()
	router.LoadHTMLGlob("templates/**/*")
	router.Static("/static", "static")

	router.GET("/", RootHandler)
	router.GET("/api/v1/users", GetUsersHandler)
	router.GET("/api/v1/user/*username", GetUserInfoHandler)
	router.Run(":9000")

}
