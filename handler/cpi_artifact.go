package handler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mmt-delivery/db"
	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)


// resolveCpiDestination looks up a tenant by ID from the DB and returns its PirApiDestinationName.
// Writes an appropriate error response and returns ("", false) on any failure.
func (h *Handler) resolveCpiDestination(ctx *gin.Context) (string, bool) {
	idStr := ctx.Query("tenant")
	if idStr == "" {
		Fail(ctx, 400, "missing required query param: tenant (tenant ID)")
		return "", false
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		Fail(ctx, 400, fmt.Sprintf("invalid tenant id %q: %s", idStr, err))
		return "", false
	}
	var tenant db.CpiTenant
	if err := h.db.First(&tenant, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			Fail(ctx, 404, fmt.Sprintf("tenant %d not found", id))
		} else {
			Fail(ctx, 500, err.Error())
		}
		return "", false
	}
	if tenant.PirApiDestinationName == "" {
		Fail(ctx, 400, fmt.Sprintf("tenant %d has no CPI destination configured (bootstrap required)", id))
		return "", false
	}
	return tenant.PirApiDestinationName, true
}

// get all packages within a cpi tenant
func (h *Handler) GetPackagesHandler(ctx *gin.Context) {
	destName, ok := h.resolveCpiDestination(ctx)
	if !ok {
		return
	}
	cpiClient, err := h.cpi.Get(ctx, destName)
	if err != nil {
		h.logger.Errorf("error while retrieving packages: %s", err)
		Fail(ctx, 500, err.Error())
		return
	}
	packages, err := cpiClient.GetPackages(ctx)
	if err != nil {
		h.logger.Errorf("error while retrieving packages: %s", err)
		Fail(ctx, 500, err.Error())
		return
	}
	OK(ctx, packages)
}


// do not return entire destination instance, hide credentials
type DestinationResp struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Url  string `json:"url"`
}

func (h *Handler) GetDestinationsHandler(ctx *gin.Context) {
	dests, err := h.destSvc.ListDestinations(ctx)
	if err != nil {
		h.logger.Errorf("error fetching destinations: %s", err)
		Fail(ctx, 500, err.Error())
		return
	}
	var destList []DestinationResp
	for _, v := range dests {
		destList = append(destList, DestinationResp{
			Name: v.Name,
			Type: v.Type,
			Url:  v.URL,
		})
	}
	OK(ctx, destList)
}

// GetPackageArtifactsHandler returns all design-time artifacts (IFlows + ScriptCollections)
// for one or more packages, merged into a result list per package.
// Concurrency is limited to avoid CPI API rate limiting (429).
// Partial success: failed packages are reported with error messages rather than aborting.
//
// GET /api/v1/tenant/packages/artifacts?tenant=<tenantID>&packages=<pkg1,pkg2,...>
func (h *Handler) GetPackageArtifactsHandler(ctx *gin.Context) {
	destName, ok := h.resolveCpiDestination(ctx)
	if !ok {
		return
	}
	packagesParam := ctx.Query("packages")
	if packagesParam == "" {
		Fail(ctx, 400, "missing required query param: packages (comma-separated package IDs)")
		return
	}
	packageIDs := strings.Split(packagesParam, ",")

	cpiClient, err := h.cpi.Get(ctx, destName)
	if err != nil {
		Fail(ctx, 500, fmt.Sprintf("failed to create CPI client: %s", err))
		return
	}

	const maxConcurrency = 3
	results := service.GetPackageArtifactsBatch(ctx, cpiClient, packageIDs, maxConcurrency)
	OK(ctx, results)
}

func (h *Handler) GetRuntimeArtifacts(ctx *gin.Context) {
	destName, ok := h.resolveCpiDestination(ctx)
	if !ok {
		return
	}
	client, err := h.cpi.Get(ctx, destName)
	if err != nil {
		Fail(ctx, 500, fmt.Sprintf("failed to create cpi client: %s", err))
		return
	}
	artifacts, err := client.GetRuntimeArtifacts(ctx)
	if err != nil {
		Fail(ctx, 500, fmt.Sprintf("failed to get runtime artifacts: %s", err))
		return
	}
	OK(ctx, artifacts)
}
