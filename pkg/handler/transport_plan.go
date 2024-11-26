package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/cpi"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/pkg/tms"
	"gopkg.in/yaml.v3"
)

// story:
// - new UI card to config transport and deploy group(ctest ctest01, prod prod01-06)
// - parse yaml file from github or plain yaml text
// - a UI view to control the process:
//   Step card 1:
//     - select branch name, e.g. hotfix, production, etc. the branch will include cpi and tms nodes
//     - paste yaml
//     - display trs, iflows, script collections parsed from the yaml. If parsing failed display error message
//   Step Card 2:
//     - check if tr numbers exist in the nodes(prod, prod01, prod02, etc). if check passes, enable trigger button
//     - a button to TRIGGER the trs, return and display import job name
//   Step Card 3:
//     - check if iflows and script collections exist in the cpi tenant
//     - a button to TRIGGER the artifacts, return and display deploy job name

type YamlArtifact struct {
	Id       string `yaml:"id"`
	Version  string `yaml:"version"`
	Package  string `yaml:"package"`
	TrNumber int    `yaml:"trNumber"`
}

// parse yaml file then create a transport&delivery plan
// params: transportGroupId, transportPlanId
// body: yaml content
func ParseYaml(ctx *gin.Context) {
	var request struct {
		TransportGroupId   int    `json:"transportGroupId"`
		TransportGroupName string `json:"transportGroupName"`
		TransportPlanId    int    `json:"transportPlanId"`
		YamlContent        string `json:"yamlContent"`
	}
	if err := ctx.BindJSON(&request); err != nil {
		return
	}
	transportPlanId, transportGroupId, transportGroupName := request.TransportPlanId, request.TransportGroupId, request.TransportGroupName

	// get transport plan
	var transportPlan db.TransportPlan
	if err := db.Conn().First(&transportPlan, transportPlanId).Error; err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": fmt.Sprintf("error while getting transport plan %d: %s", transportPlanId, err)})
		return
	}
	// get transport group
	var transportGroup db.TransportGroup
	if err := db.Conn().First(&transportGroup, transportGroupId).Error; err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": fmt.Sprintf("error while getting transport group %d: %s", transportGroupId, err)})
		return
	}
	// parse yaml
	var yamlArtifacts map[string][]YamlArtifact
	if err := yaml.Unmarshal([]byte(request.YamlContent), &yamlArtifacts); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to parse yaml file: " + err.Error()})
		return
	}
	//gather trs and artifacts
	var trs = make([]db.TransportRequest, 0)
	var artifacts = make([]db.Artifact, 0)
	for _, iflow := range yamlArtifacts["iflows"] {
		trs = append(trs, db.TransportRequest{ID: iflow.TrNumber})
		artifacts = append(artifacts, db.Artifact{Id: iflow.Id, Version: iflow.Version, Package: iflow.Package, Type: Artifact_Type_Iflow})
	}
	for _, sc := range yamlArtifacts["scriptCollections"] {
		trs = append(trs, db.TransportRequest{ID: sc.TrNumber})
		artifacts = append(artifacts, db.Artifact{Id: sc.Id, Version: sc.Version, Package: sc.Package, Type: Artifact_Type_Sc})
	}

	// save into transport plan
	transportPlan.Artifacts = artifacts
	transportPlan.TransportRequests = trs
	transportPlan.TransportGroupID = transportGroupId
	transportPlan.TransportGroupName = transportGroupName
	transportPlan.UpdatedBy = User(ctx)
	if err := db.Conn().Updates(&transportPlan).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to save transport plan: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"msg": "Transport Plan updated"})

}

func GenerateImportJob(ctx *gin.Context) {

	transportPlanId, err := strconv.Atoi(ctx.Query("transportPlanId"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Invalid transport plan id: " + err.Error()})
		return
	}
	var transportPlan db.TransportPlan
	var transportGroup db.TransportGroup
	if err := transportPlanAndGroup(transportPlanId, &transportPlan, &transportGroup); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("error while getting transport plan %d: %s", transportPlanId, err)})
		return
	}
	// check if all trs are found in the node
	// TODO go routine
	for _, node := range transportGroup.TransportNodes {
		err := checkTrs(ctx, transportPlan.TransportRequests, node.ID)
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"msg": fmt.Sprintf("error while checking trs in node %s: %s", node.Name, err)})
			return
		}
	}
	// generate import job
	var importJob = db.Job{
		Name:        fmt.Sprintf("[import] %s", transportPlan.Name),
		Description: transportPlan.Description,
		Status:      JOB_STATUS_SAVED,
		Type:        Job_Type_Import,
		CreatedBy:   User(ctx),
		UpdatedBy:   User(ctx),
	}
	if err := createJobSrv(&importJob, User(ctx)); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create import job: " + err.Error()})
		return
	}
	// save import job id into transport plan
	transportPlan.ImportJobId = importJob.ID
	if err := db.Conn().Save(&transportPlan).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to update transport plan: " + err.Error()})
		return
	}
	// generate import steps
	for i, transportNode := range transportGroup.TransportNodes {
		var importStep = db.ImportStep{
			JobId:                importJob.ID,
			Sequence:             uint(i),
			Status:               STEP_STATUS_SAVED,
			TransportNodeId:      uint(transportNode.ID),
			TransportNodeName:    transportNode.Name,
			TransportRequests_V2: transportPlan.TransportRequests,
			UpdatedBy:            User(ctx),
			Type:                 Step_Type_Import,
		}
		if err := db.Conn().Create(&importStep).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create import step: " + err.Error()})
			return
		}

	}

}

func GenerateDeployJob(ctx *gin.Context) {
	transportPlanId, err := strconv.Atoi(ctx.Query("transportPlanId"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Invalid transport plan id: " + err.Error()})
		return
	}
	// get transport plan and group by transport plan id
	var transportPlan db.TransportPlan
	var transportGroup db.TransportGroup
	if err := transportPlanAndGroup(transportPlanId, &transportPlan, &transportGroup); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": fmt.Sprintf("error while getting transport plan %d: %s", transportPlanId, err)})
		return
	}
	// check if artifacts exists
	for _, endpoint := range transportGroup.DeployEndpoints {
		if err := checkArtifacts(ctx, endpoint, transportPlan.Artifacts); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"msg": fmt.Sprintf("check artifacts failed in cpi tenant %s: %s", endpoint, err)})
			return
		}
	}
	// generate deploy job
	var deployJob = db.Job{
		Name:        fmt.Sprintf("[deploy] %s", transportPlan.Name),
		Description: transportPlan.Description,
		Status:      JOB_STATUS_SAVED,
		Type:        Job_Type_Deploy,
		CreatedBy:   User(ctx),
		UpdatedBy:   User(ctx),
	}
	if err := db.Conn().Create(&deployJob).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create deploy job: " + err.Error()})
		return
	}
	transportPlan.DeployJobId = deployJob.ID
	if err := db.Conn().Save(&transportPlan).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to update transport plan: " + err.Error()})
		return
	}
	// generate deploy steps
	for i, endpoint := range transportGroup.DeployEndpoints {
		var deployStep = db.DeployStep{
			JobId:     deployJob.ID,
			Sequence:  uint(i),
			Status:    STEP_STATUS_SAVED,
			Endpoint:  endpoint,
			Artifacts: transportPlan.Artifacts,
			UpdatedBy: User(ctx),
			Type:      Step_Type_Deploy,
		}
		if err := db.Conn().Create(&deployStep).Error; err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create deploy step: " + err.Error()})
			return
		}
	}

}

// check if artifacts exists
func checkArtifacts(ctx context.Context, tenant string, artifacts []db.Artifact) error {
	cpiClient, err := cpi.NewClient(ctx, tenant)
	if err != nil {
		return fmt.Errorf("cpi client %s creation error: %s", tenant, err)
	}
	// check if artifact exists in the tenant
	for _, artifact := range artifacts {
		if artifact.Type == Artifact_Type_Iflow {
			if _, err := cpiClient.GetPackageIflow(artifact.Package, artifact.Id, artifact.Version); err != nil {
				return fmt.Errorf("integration iflow %s not found in tenant %s: %s", artifact.Id, tenant, err)
			}
		} else if artifact.Type == Artifact_Type_Sc {
			if _, err := cpiClient.GetScriptCollection(artifact.Id, artifact.Version); err != nil {
				return fmt.Errorf("script collection artifact %s not found in tenant %s: %s", artifact.Id, tenant, err)
			}
		}
	}
	return nil
}

// check if all trs are found in the node. save the description
func checkTrs(ctx context.Context, trs []db.TransportRequest, nodeId int) error {
	targetTrs := make(map[int]*db.TransportRequest)
	for i, tr := range trs {
		targetTrs[tr.ID] = &trs[i]
	}
	tmsClient, err := tms.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create tms client: %s", err)
	}
	nodeTrs, err := tmsClient.GetNodeTransportRequests(nodeId)
	if err != nil {
		return fmt.Errorf("failed to get node trs: %s", err)
	}
	// check tr number exists in the tms node
	for _, nodeTr := range nodeTrs {
		if _, ok := targetTrs[nodeTr.ID]; ok {
			targetTrs[nodeTr.ID].Description = nodeTr.Description
		}
	}
	for i, tr := range targetTrs {
		if tr.Description == "" {
			return fmt.Errorf("transport request id not found: %d", i)
		}
	}
	return nil
}

// get transport plan and group by transport plan id
func transportPlanAndGroup(transportPlanId int, transportPlan *db.TransportPlan, transportGroup *db.TransportGroup) error {
	if err := db.Conn().First(transportPlan, transportPlanId).Error; err != nil {
		return fmt.Errorf("error while getting transport plan %d: %s", transportPlanId, err)
	}
	if err := db.Conn().First(transportGroup, transportPlan.TransportGroupID).Error; err != nil {
		return fmt.Errorf("error while getting transport group %d: %s", transportPlan.TransportGroupID, err)
	}
	return nil
}

// create or update a transport plan
func SaveTransportPlan(ctx *gin.Context) {
	var plan db.TransportPlan
	if err := ctx.ShouldBindJSON(&plan); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Invalid request: " + err.Error()})
		return
	}
	user := User(ctx)
	plan.UpdatedBy = user
	// create/update use the same handler, so plan id of 0 means creating a new transport plan
	if plan.ID == 0 {
		plan.CreatedBy = user
	}
	if err := db.Conn().Save(&plan).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to create transport plan: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"msg": "Transport plan Updated"})
}

// get all transport plans
func GetAllTransportPlans(ctx *gin.Context) {
	var plans []db.TransportPlan
	if err := db.Conn().Find(&plans).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to get transport plans: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"result": plans})
}

// get a transport plan by id
func GetTransportPlan(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Invalid transport plan id: " + err.Error()})
		return
	}
	var plan db.TransportPlan
	if err := db.Conn().First(&plan, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to get transport plan: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"result": plan})
}

// delete a transport plan by id
func DeleteTransportPlan(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"msg": "Invalid transport plan id: " + err.Error()})
		return
	}
	if err := db.Conn().Delete(&db.TransportPlan{}, id).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"msg": "Failed to delete transport plan: " + err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"msg": fmt.Sprintf("transport plan %d deleted successfully", id)})
}
