package handler

import (
	"fmt"
	"net/http"
	"strings"

	. "mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"
	"mmt-delivery/pkg/env"

	"github.com/gin-gonic/gin"
)

var logger = env.Logger()

// get all packages within a cpi tenant
func GetPackagesHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	cpiClient, error := cpi.NewClient(ctx, cpi_tenant)
	if error != nil {
		logger.Error("error while retrieving packages: %s", error)
		return
	}
	packages, error := cpiClient.GetPackages(ctx)
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
	iflows, error := cpiClient.GetPackageIflows(ctx, packageID)
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

// include type: script collection, iflow artifacts
// TODO: may call cpi-cookie-service to get all artifacts in one call
func GetPackageArtifactsHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	packageID := ctx.Query("package")
	client, err := cpi.NewClient(ctx, cpi_tenant)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "result": fmt.Sprintf("failed to create cpi client: %s", err)})
		return
	}
	artifactResp := make([]db.Artifact, 0)
	iflows, err := client.GetPackageIflows(ctx, packageID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "result": fmt.Sprintf("failed to get iflows: %s", err)})
		return
	}
	scriptColls, err := client.GetPackageScriptcollections(ctx, packageID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "result": fmt.Sprintf("failed to get script collections: %s", err)})
		return
	}
	for _, v := range scriptColls {
		artifactResp = append(artifactResp, wrapArtifact(Artifact_Type_Sc, v))
	}
	for _, v := range iflows {
		artifactResp = append(artifactResp, wrapArtifact("Integration Flow", v))
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": artifactResp,
	})

}

// wrapArtifact normalizes CPI items (script collection or iflow) into a db.Artifact DTO (not persisted here).
// Both ScriptCollectionItem and IflowItem embed ArtifactCommonItem so we only need those fields.
func wrapArtifact(artifactType ArtifactType, artifact any) db.Artifact {
	switch v := artifact.(type) {
	case cpi.ScriptCollectionItem:
		return db.Artifact{
			TechID:      v.ID,
			Version:     v.Version,
			PackageID:   v.PackageID,
			Name:        v.Name,
			Description: v.Description,
			Type:        artifactType,
			CreatedBy:   v.CreatedBy,
			CreatedAt:   v.CreatedAt,
			ModifiedBy:  v.ModifiedBy,
			ModifiedAt:  v.ModifiedAt,
		}
	case cpi.IflowItem:
		return db.Artifact{
			TechID:      v.ID,
			Version:     v.Version,
			PackageID:   v.PackageID,
			Name:        v.Name,
			Description: v.Description,
			Type:        artifactType,
			CreatedBy:   v.CreatedBy,
			CreatedAt:   v.CreatedAt,
			ModifiedBy:  v.ModifiedBy,
			ModifiedAt:  v.ModifiedAt,
		}
	default:
		return db.Artifact{Type: artifactType}
	}
}

// do not return entire destination instance, hide credentials
type DestinationResp struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Url  string `json:"url"`
}

func GetDestinationsHandler(ctx *gin.Context) {
	var destList []DestinationResp
	for i, v := range env.Destinations() {
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

// Get all deployed(runtime) artifacts by cpi tenant
func GetRuntimeArtifacts(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	if cpi_tenant == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "bad request: missing tenant"})
		return
	}
	client, err := cpi.NewClient(ctx, cpi_tenant)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("failed to create cpi client: %s", err)})
		return
	}
	artifacts, err := client.GetRuntimeArtifacts(ctx)
	if err != nil {
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": artifacts,
	})
}
