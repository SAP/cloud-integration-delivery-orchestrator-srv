package handler

import (
	"mmt-delivery/pkg/cpi"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

type DeliverRequest struct {
	ArtifactID      string `json:"artifact_id"`
	ArtifactVersion string `json:"artifacrt_version"`
	ArtifactType    string `json:"artifact_type"`
	CPITenant       string `json:"cpi_tenant"`
	DeliverComment  string `json:"deliver_comment"` // should contain JIRA info or other comments
}

// deliver Artifacts natively, don't use transport plan, directly upload artifacts to target tenant
func NativeDeliver(ctx *gin.Context) {
	var deliverRequest DeliverRequest
	if err := ctx.BindJSON(&deliverRequest); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Failed to bind native deliver request json: " + err.Error()})
		return
	}

	var packageID, branch, modifiedBy, modifiedAt string

	client, err := cpi.NewClient(ctx, deliverRequest.CPITenant)
	if err != nil {
		// todo
		return
	}

	branch = parseTenant(client.ApiURL) // use tenant subdomain as branch name

	if deliverRequest.ArtifactType == "ScriptCollection" {
		scItem, err := client.GetScriptCollection(deliverRequest.ArtifactID, deliverRequest.ArtifactVersion)
		if err != nil {
			return
		}
		packageID = scItem.PackageID
		modifiedBy, modifiedAt = scItem.ModifiedBy, scItem.ModifiedAt

	} else if deliverRequest.ArtifactType == "IntegrationFlow" {
		iflowItem, err := client.GetIflow(deliverRequest.ArtifactID, deliverRequest.ArtifactVersion)
		if err != nil {
			return
		}
		packageID = iflowItem.PackageID
		modifiedBy, modifiedAt = iflowItem.ModifiedBy, iflowItem.ModifiedAt

	}

	if err := client.DownloadArtifact(deliverRequest.ArtifactID, deliverRequest.ArtifactVersion, packageID, deliverRequest.ArtifactType); err != nil {
		ctx.JSON(500, gin.H{"error": "Failed to download artifact:" + err.Error()})
		return
	}

	if err := client.SyncToGithub(deliverRequest.ArtifactID, deliverRequest.ArtifactVersion, deliverRequest.ArtifactType, packageID, branch, modifiedBy, modifiedAt, deliverRequest.DeliverComment); err != nil {
		ctx.JSON(500, gin.H{"error": "Failed to sync Artifact to github:" + err.Error()})
		return
	}

}

func parseTenant(uri string) string {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		panic("Invalid URL")
	}

	host := parsedURL.Host
	subdomain := strings.Split(host, ".")[0]
	return subdomain
}
