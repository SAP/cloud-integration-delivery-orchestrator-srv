package handler

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type NativeDeliverRequest struct {
	Artifacts []struct {
		ArtifactID      string `json:"artifact_id"`
		ArtifactVersion string `json:"artifact_version"`
		ArtifactType    string `json:"artifact_type"`
	} `json:"artifacts"`

	SrcCpiTenant   string   `json:"src_cpi_tenant"`  // source CPI tenant, e.g. "CPIAPI_DEV"
	DestCpiTenants []string `json:"dest_cpi_tenant"` // destination CPI tenants, e.g. ["CPIAPI_QA", "CPIAPI_PROD"]
	DeliverComment string   `json:"deliver_comment"` // should contain JIRA info or other comments
}

// deliver Artifacts natively, avoid to use transport plan, directly upload artifacts to target tenants
func (h *Handler) NativeDeliver(ctx *gin.Context) {
	var deliverRequest NativeDeliverRequest
	if err := ctx.BindJSON(&deliverRequest); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Failed to bind native deliver request json: " + err.Error()})
		return
	}

	srcClient, err := h.cpi.Get(ctx, deliverRequest.SrcCpiTenant)
	if err != nil {
		ctx.JSON(500, gin.H{"error": "Failed to create CPI client: " + err.Error()})
		return
	}

	srcBranch := parseTenant(srcClient.ApiURL) // use tenant subdomain as branch name

	for _, artifact := range deliverRequest.Artifacts {
		var packageID, modifiedBy, modifiedAt string

		switch artifact.ArtifactType {
		case "ScriptCollection":
			scItem, err := srcClient.GetDesignTimeScriptCollection(ctx, artifact.ArtifactID, artifact.ArtifactVersion)
			if err != nil {
				ctx.JSON(500, gin.H{"error": "Failed to get script collection:" + err.Error()})
				return
			}
			packageID = scItem.PackageID
			modifiedBy, modifiedAt = scItem.ModifiedBy, scItem.ModifiedAt

		case "IntegrationFlow":
			iflowItem, err := srcClient.GetDesignTimeIflow(ctx, artifact.ArtifactID, artifact.ArtifactVersion)
			if err != nil {
				ctx.JSON(500, gin.H{"error": "Failed to get integration flow:" + err.Error()})
				return
			}
			packageID = iflowItem.PackageID
			modifiedBy, modifiedAt = iflowItem.ModifiedBy, iflowItem.ModifiedAt
		}

		if err := srcClient.DownloadArtifact(ctx, artifact.ArtifactID, artifact.ArtifactVersion, packageID, artifact.ArtifactType); err != nil {
			ctx.JSON(500, gin.H{"error": "Failed to download artifact:" + err.Error()})
			return
		}

		if err := srcClient.SyncToGithub(artifact.ArtifactID, artifact.ArtifactVersion, artifact.ArtifactType, packageID, srcBranch, modifiedBy, modifiedAt, deliverRequest.DeliverComment); err != nil {
			ctx.JSON(500, gin.H{"error": "Failed to sync Artifact to github:" + err.Error()})
			return
		}

		// TODO: upload to SAP JFrog
		// No need to publish Github Release, but upload to JFrog instead. github release does not meet demand,
		// since it will zip and relese the whole repository at the same time, along with this artifact.

		// deliver to destination tenants
		// TODO: parallel
		for _, destTenant := range deliverRequest.DestCpiTenants {
			destClient, err := h.cpi.Get(ctx, destTenant)
			if err != nil {
				ctx.JSON(500, gin.H{"error": "Failed to create destination CPI client: " + err.Error()})
				return
			}

			if err := destClient.UploadArtifact(ctx, artifact.ArtifactID, artifact.ArtifactVersion, packageID, artifact.ArtifactType); err != nil {
				ctx.JSON(500, gin.H{"error": "Failed to upload artifact to destination tenant: " + err.Error()})
				return
			}

			// TODO: deploy the artifact by artifact type

			// TODO: loop for aroud 5 times to check deploy stus
			for i := 0; i < 5; i++ {
				runtimeArtifact, err := destClient.RuntimeArtifact(ctx, artifact.ArtifactID)
				if err != nil {
					ctx.JSON(500, gin.H{"error": "Failed to check deploy status on destination tenant: " + err.Error()})
					return
				}
				if runtimeArtifact.ID != "" && runtimeArtifact.Status == "STARTED" {
					break // deployed successfully
				}
				if i < 4 {
					// sleep 10 seconds before next check
					time.Sleep(10 * time.Second)
				}
			}

		}

	}

}

// use subdomain as tenant name
func parseTenant(uri string) string {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		panic("Invalid URL")
	}

	host := parsedURL.Host
	subdomain := strings.Split(host, ".")[0]
	return subdomain
}
