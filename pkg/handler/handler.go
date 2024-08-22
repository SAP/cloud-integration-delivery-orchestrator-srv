package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
)

var logger = log.NewLogger().Sugar()
var tmsEndpoint db.ApiEndpoint

// init. get default TMS api Endpoint
func init() {
	ctx := context.Background()
	conn, error := pgx.Connect(ctx, DBSource)
	if error != nil {
		logger.Panic("Failed to connect to DB: %s", error)
		return
	}
	query := db.New(conn)
	tmsEndpoint, error = query.GetApiEndpointById(ctx, 3)
	if error != nil {
		logger.Panic("Failed to fetch default tms endpoint: %s", error)
	}

}

// get all packages within a cpi tenant
func GetPackagesHandler(ctx *gin.Context) {
	context := ctx.Request.Context()
	dbClient := NewDBClient(ctx)
	cpi_tenant := ctx.Query("tenant")

	cpi_id, _ := strconv.Atoi(cpi_tenant)
	query := db.New(dbClient.DBConn)
	cpiEndpoint, _ := query.GetApiEndpointById(context, cpi_id)
	cpiClient, error := cpi.NewCPIClient(context, cpiEndpoint.ClientId, cpiEndpoint.ClientSecret, cpiEndpoint.AuthUrl, cpiEndpoint.ApiUrl)
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
	cpiEndpoint, _ := query.GetApiEndpointById(context, cpi_id)
	cpiClient, _ := cpi.NewCPIClient(context, cpiEndpoint.ClientId, cpiEndpoint.ClientSecret, cpiEndpoint.AuthUrl, cpiEndpoint.ApiUrl)
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

func GetTmsNodesHandler(ctx *gin.Context) {

	tmsClient, error := tms.NewTMSClient(ctx, tmsEndpoint.ClientId, tmsEndpoint.ClientSecret, tmsEndpoint.AuthUrl, tmsEndpoint.ApiUrl)
	if error != nil {
		return
	}
	tmsNodes, error := tmsClient.GetNodes()
	if error != nil {
		errorMsg := fmt.Sprintf("Error while retrieving tms Nodes: %s", error)
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "result": errorMsg})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": tmsNodes})
	return
}

func GetTranportRequestsHandler(ctx *gin.Context) {
	trNode := ctx.Query("transportNode")
	nodeId, error := strconv.Atoi(trNode)
	if error != nil {
		errorMsg := fmt.Sprintf("Invalid transport node id %d: %s", trNode, error)
		logger.Error(errorMsg)
		ctx.JSON(http.StatusOK, gin.H{"status": 500, "result": errorMsg})
		return
	}

	tmsClient, error := tms.NewTMSClient(ctx, tmsEndpoint.ClientId, tmsEndpoint.ClientSecret, tmsEndpoint.AuthUrl, tmsEndpoint.ApiUrl)
	if error != nil {
		logger.Error(error)
		return
	}
	trs, error := tmsClient.GetNodeTransportRequests(nodeId)
	if error != nil {
		errorMsg := fmt.Sprintf("Error while get node trs: %s", error)
		logger.Error(errorMsg)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": trs})

}
