package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/remotecall"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
)

var logger = log.NewLogger().Sugar()

// get all packages within a cpi tenant
func GetPackagesHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	cpiClient, error := cpi.NewClient(ctx, cpi_tenant)
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
	cpi_tenant := ctx.Query("tenant")
	packageID := ctx.Query("package")

	cpiClient, _ := cpi.NewClient(context, cpi_tenant)
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

	tmsClient, error := tms.NewClient(ctx)
	if error != nil {
		return
	}
	tmsNodes, error := tmsClient.GetNodes()
	if error != nil {
		errorMsg := fmt.Sprintf("Error while retrieving tms Nodes: %s", error)
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 500, "result": errorMsg})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"status": 200, "result": tmsNodes})
	return
}

func GetTranportRequestsHandler(ctx *gin.Context) {
	trNode := ctx.Query("transportNode")
	nodeId, error := strconv.Atoi(trNode)
	if error != nil {
		errorMsg := fmt.Sprintf("Invalid transport node id %s: %s", trNode, error)
		logger.Error(errorMsg)
		ctx.JSON(http.StatusBadRequest, gin.H{"status": 500, "result": errorMsg})
		return
	}

	tmsClient, error := tms.NewClient(ctx)
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

type DestinationResp struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Url  string `json:"url"`
}

func GetDestinationsHandler(ctx *gin.Context) {
	var destList []DestinationResp
	for i, v := range remotecall.DestEnv() {
		if strings.HasPrefix(i, "DEST_CPIAPI") {
			destList = append(destList, DestinationResp{
				Name: v.Name,
				Type: v.Type,
				Url:  v.URL,
			})
		}
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": destList,
	})
}
