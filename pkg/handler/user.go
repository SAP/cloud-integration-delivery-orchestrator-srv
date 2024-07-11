package handler

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

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
		ctx.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": errUer})

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

func CreateUsersHandler(ctx *gin.Context) {
	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, dbSource)
	if errDBconn != nil {
		log.Fatal("error connecting to db,", errDBconn)
	}
	query := db.New(dbConn)
	var user db.CreateUserParams
	err := ctx.BindJSON(&user)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"code": 404, "msg": err})
	} else {
		users, errUer := query.CreateUser(context, user)
		if errUer != nil {
			ctx.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "user not found"})
		} else {
			ctx.JSON(http.StatusOK, gin.H{"code": 200, "result": users})
		}
	}

}
