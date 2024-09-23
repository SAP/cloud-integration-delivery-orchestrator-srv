package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/log"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/remotecall"
)

var logger = log.NewLogger().Sugar()

// get all packages within a cpi tenant
func GetPackagesHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	cpiClient, error := cpi.NewClient(ctx, cpi_tenant)
	if error != nil {
		logger.Error("error while retrieving packages: %s", error)
		return
	}
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
	cpi_tenant := ctx.Query("tenant")
	packageID := ctx.Query("package")

	cpiClient, _ := cpi.NewClient(ctx, cpi_tenant)
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

type ArtifactResp struct {
	ID          string `json:"Id"`
	Version     string `json:"Version"`
	PackageID   string `json:"PackageId"`
	Name        string `json:"Name"`
	Description string `json:"Description"`
	Type        string `json:"Type"`
}

// include type: script collection, iflow artifacts
func GetPackageArtifactsHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	packageID := ctx.Query("package")
	client, err := cpi.NewClient(ctx, cpi_tenant)
	if err != nil {
		return
	}
	artifactResp := make([]ArtifactResp, 0)
	iflows, err := client.GetPackageIflows(packageID)
	if err != nil {
		return
	}
	scriptColls, err := client.GetPackageScriptcollections(packageID)
	if err != nil {
		return
	}
	for _, v := range scriptColls {
		artifactResp = append(artifactResp, ArtifactResp{
			ID:          v.ID,
			Version:     v.Version,
			PackageID:   v.PackageID,
			Name:        v.Name,
			Description: v.Description,
			Type:        "Script Collection",
		})
	}
	for _, v := range iflows {
		artifactResp = append(artifactResp, ArtifactResp{
			ID:          v.ID,
			Version:     v.Version,
			PackageID:   v.PackageID,
			Name:        v.Name,
			Description: v.Description,
			Type:        "Integration Flow",
		})
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": artifactResp,
	})

}

// do not return entire destination instance, hide credentials
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
