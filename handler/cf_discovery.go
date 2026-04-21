package handler

import (
	"github.com/gin-gonic/gin"
	"mmt-delivery/pkg/cf"
)

// CfNamedResource is a minimal org or space representation returned to the UI.
type CfNamedResource struct {
	GUID string `json:"guid"`
	Name string `json:"name"`
}

// ExchangeCfPasscode exchanges a one-time CF passcode for a short-lived Bearer
// token.  The token is returned to the caller and is never persisted server-side.
//
// POST /api/v1/cf/token
func (h *Handler) ExchangeCfPasscode(ctx *gin.Context) {
	var body struct {
		CfApiEndpoint string `json:"cfApiEndpoint" binding:"required"`
		Passcode      string `json:"passcode"      binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "cfApiEndpoint and passcode are required")
		return
	}

	token, err := cf.ExchangeCfPasscode(ctx.Request.Context(), body.CfApiEndpoint, body.Passcode)
	if err != nil {
		Fail(ctx, 400, err.Error())
		return
	}
	OK(ctx, gin.H{"accessToken": token})
}

// ListCfOrgs proxies a GET /v3/organizations call to the CF API using the
// operator-supplied Bearer token.  The token is never persisted.
//
// POST /api/v1/cf/orgs
func (h *Handler) ListCfOrgs(ctx *gin.Context) {
	var body struct {
		CfApiEndpoint string `json:"cfApiEndpoint" binding:"required"`
		CfToken       string `json:"cfToken"       binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "cfApiEndpoint and cfToken are required")
		return
	}

	cfClient, err := cf.NewCFClient(body.CfApiEndpoint, body.CfToken)
	if err != nil {
		Fail(ctx, 400, "failed to connect to CF API: "+err.Error())
		return
	}

	orgs, err := cfClient.ListOrgs(ctx.Request.Context())
	if err != nil {
		status := 502
		if code := cf.HTTPStatusCode(err); code == 401 || code == 403 {
			status = 400
		}
		Fail(ctx, status, "CF org list failed: "+err.Error())
		return
	}

	result := make([]CfNamedResource, 0, len(orgs))
	for _, o := range orgs {
		result = append(result, CfNamedResource{GUID: o.GUID, Name: o.Name})
	}
	OK(ctx, result)
}

// ListCfSpaces proxies a GET /v3/spaces?organization_guids=<orgGuid> call to
// the CF API using the operator-supplied Bearer token.
//
// POST /api/v1/cf/spaces
func (h *Handler) ListCfSpaces(ctx *gin.Context) {
	var body struct {
		CfApiEndpoint string `json:"cfApiEndpoint" binding:"required"`
		CfToken       string `json:"cfToken"       binding:"required"`
		OrgGUID       string `json:"orgGuid"       binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil {
		Fail(ctx, 400, "cfApiEndpoint, cfToken, and orgGuid are required")
		return
	}

	cfClient, err := cf.NewCFClient(body.CfApiEndpoint, body.CfToken)
	if err != nil {
		Fail(ctx, 400, "failed to connect to CF API: "+err.Error())
		return
	}

	spaces, err := cfClient.ListSpaces(ctx.Request.Context(), body.OrgGUID)
	if err != nil {
		status := 502
		if code := cf.HTTPStatusCode(err); code == 401 || code == 403 {
			status = 400
		}
		Fail(ctx, status, "CF space list failed: "+err.Error())
		return
	}

	result := make([]CfNamedResource, 0, len(spaces))
	for _, s := range spaces {
		result = append(result, CfNamedResource{GUID: s.GUID, Name: s.Name})
	}
	OK(ctx, result)
}
