package handler

import (
	"fmt"
	"net/http"
	"strings"

	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
)

// get all packages within a cpi tenant
func (h *Handler) GetPackagesHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	cpiClient, error := h.cpi.Get(ctx, cpi_tenant)
	if error != nil {
		h.logger.Errorf("error while retrieving packages: %s", error)
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": error.Error()})
		return
	}
	packages, error := cpiClient.GetPackages(ctx)
	if error != nil {
		h.logger.Errorf("error while retrieving packages: %s", error)
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": error.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"code":   200,
		"result": packages,
	})

}

// get all iflows under a package
func (h *Handler) GetPackageIflowsHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	packageID := ctx.Query("package")

	cpiClient, err := h.cpi.Get(ctx, cpi_tenant)
	if err != nil {
		h.logger.Errorf("error creating CPI client: %s", err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "error": err.Error()})
		return
	}
	iflows, error := cpiClient.GetPackageIflows(ctx, packageID)
	if error != nil {
		h.logger.Error("Error while retrieving iflows within a package")
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": 500, "result": fmt.Sprintf("internal server error: %s", error)})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"status": "success",
		"result": iflows,
	})
}

// include type: script collection, iflow artifacts
// Delegates to service.FetchPackageArtifacts for unified artifact retrieval.
func (h *Handler) GetPackageArtifactsHandler(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	packageID := ctx.Query("package")
	client, err := h.cpi.Get(ctx, cpi_tenant)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "result": fmt.Sprintf("failed to create cpi client: %s", err)})
		return
	}
	artifacts, err := service.FetchPackageArtifacts(ctx, client, packageID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"status": http.StatusInternalServerError, "result": fmt.Sprintf("failed to get artifacts: %s", err)})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": artifacts,
	})
}

// do not return entire destination instance, hide credentials
type DestinationResp struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Url  string `json:"url"`
}

func (h *Handler) GetDestinationsHandler(ctx *gin.Context) {
	var destList []DestinationResp
	for i, v := range h.destinations {
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
func (h *Handler) GetRuntimeArtifacts(ctx *gin.Context) {
	cpi_tenant := ctx.Query("tenant")
	if cpi_tenant == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "bad request: missing tenant"})
		return
	}
	client, err := h.cpi.Get(ctx, cpi_tenant)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("failed to create cpi client: %s", err)})
		return
	}
	artifacts, err := client.GetRuntimeArtifacts(ctx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("failed to get runtime artifacts: %s", err)})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"result": artifacts,
	})
}
