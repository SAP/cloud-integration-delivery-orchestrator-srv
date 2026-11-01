package handler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cpi"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/service"

	"github.com/gin-gonic/gin"
)


// resolveCpiClient parses the tenant ID from the query string, gets a CPI client
// from the manager, and returns it. Writes an appropriate error response on failure.
func (h *Handler) resolveCpiClient(ctx *gin.Context) (cpi.IntegrationService, bool) {
	idStr := ctx.Query("tenant")
	if idStr == "" {
		Fail(ctx, 400, "missing required query param: tenant (tenant ID)")
		return nil, false
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		Fail(ctx, 400, fmt.Sprintf("invalid tenant id %q: %s", idStr, err))
		return nil, false
	}
	client, err := h.svc.CPI.Get(ctx, uint(id))
	if err != nil {
		if errors.Is(err, cpi.ErrNoDestination) {
			Fail(ctx, 400, fmt.Sprintf("tenant %d has no CPI destination configured (bootstrap required)", id))
		} else {
			Fail(ctx, 500, err.Error())
		}
		return nil, false
	}
	return client, true
}

// get all packages within a cpi tenant
func (h *Handler) GetPackagesHandler(ctx *gin.Context) {
	cpiClient, ok := h.resolveCpiClient(ctx)
	if !ok {
		return
	}
	packages, err := cpiClient.GetPackages(ctx)
	if err != nil {
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
// Partial success: failed packages are reported with error messages rather than aborting.
//
// GET /api/v1/tenant/packages/artifacts?tenant=<tenantID>&packages=<pkg1,pkg2,...>
func (h *Handler) GetPackageArtifactsHandler(ctx *gin.Context) {
	cpiClient, ok := h.resolveCpiClient(ctx)
	if !ok {
		return
	}
	packagesParam := ctx.Query("packages")
	if packagesParam == "" {
		Fail(ctx, 400, "missing required query param: packages (comma-separated package IDs)")
		return
	}
	packageIDs := strings.Split(packagesParam, ",")

	results := service.GetPackageArtifactsBatch(ctx, cpiClient, packageIDs)
	OK(ctx, results)
}

func (h *Handler) GetRuntimeArtifacts(ctx *gin.Context) {
	client, ok := h.resolveCpiClient(ctx)
	if !ok {
		return
	}
	artifacts, err := client.GetRuntimeArtifacts(ctx)
	if err != nil {
		Fail(ctx, 500, fmt.Sprintf("failed to get runtime artifacts: %s", err))
		return
	}
	OK(ctx, artifacts)
}
