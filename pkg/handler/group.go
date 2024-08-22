package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

func GetGroupInfoHandler(ctx *gin.Context) {
	name := ctx.Param("groupname")
	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, DBSource)
	if errDBconn != nil {
		logger.Errorf("error connecting to db, %s", errDBconn)
	}
	query := db.New(dbConn)
	user, errUer := query.GetUser(context, name)
	if errUer != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": errUer})

	} else {
		ctx.JSON(http.StatusOK, gin.H{"code": 200, "result": user})
	}

}

func GetGroupsHandler(ctx *gin.Context) {
	context := ctx.Request.Context()
	dbConn, errDBconn := pgx.Connect(context, DBSource)
	if errDBconn != nil {
		logger.Errorf("error connecting to db, %s", errDBconn)
	}
	query := db.New(dbConn)
	users, errUer := query.GetUsers(context)
	if errUer != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "user not found"})
	} else {
		ctx.JSON(http.StatusOK, gin.H{"code": 200, "result": users})
	}

}
