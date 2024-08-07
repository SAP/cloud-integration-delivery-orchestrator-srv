package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
)

var logger = log.NewLogger().Sugar()

// get all packages within a cpi tenant
func GetPackagesHandler(ctx *gin.Context) {
	context := ctx.Request.Context()
	dbClient := NewDBClient(ctx)
	cpi_tenant := ctx.Query("tenant")

	cpi_id, _ := strconv.Atoi(cpi_tenant)
	query := db.New(dbClient.DBConn)
	cpiEndpoint, _ := query.GetConfigByID(context, cpi_id)
	cpiClient, error := cpi.NewCPIClient(context, cpiEndpoint.AuthClientID, cpiEndpoint.AuthClientSecret, cpiEndpoint.AuthUrl, cpiEndpoint.ApiUrl)
	packages, error := cpiClient.GetPackages()
	if error != nil {
		logger.Error("error while retrieving packages: %s", error)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": packages,
	})

}

// get all iflows under a package
func GetPackageIflowsHandler(ctx *gin.Context) {
	context := ctx.Request.Context()
	dbClient := NewDBClient(ctx)
	cpi_tenant := ctx.Query("tenant")
	packageID := ctx.Query("package")

	cpi_id, _ := strconv.Atoi(cpi_tenant)
	query := db.New(dbClient.DBConn)
	cpiEndpoint, _ := query.GetConfigByID(context, cpi_id)
	cpiClient, _ := cpi.NewCPIClient(context, cpiEndpoint.AuthClientID, cpiEndpoint.AuthClientSecret, cpiEndpoint.AuthUrl, cpiEndpoint.ApiUrl)
	iflows, error := cpiClient.GetPackageIflows(packageID)
	if error != nil {
		logger.Error("Error while retrieving iflows within a package")
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "result": fmt.Sprintf("internal server error: %s", error)})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"result": iflows,
	})

}
