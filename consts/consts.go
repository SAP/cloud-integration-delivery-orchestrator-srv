package consts

import "time"

type ArtifactType string

type RuntimeState string

const (
	STEP_STATUS_ERROR   = "Error"
	STEP_STATUS_RUNNING = "Running"
	STEP_STATUS_SUCCESS = "Success"
	STEP_STATUS_SAVED   = "Saved"
	STEP_STATUS_DRAFT   = "Draft"

	JOB_STATUS_ERROR   = "Error"
	JOB_STATUS_RUNNING = "Running"
	JOB_STATUS_SUCCESS = "Success"
	JOB_STATUS_UNKNOWN = "Unknown"
	JOB_STATUS_SAVED   = "Saved"

	DEPLOY_STATUS_SUCCESS               = "SUCCESS"
	DEPLOY_STATUS_FAIL                  = "FAIL"
	DEPLOY_STATUS_DEPLOYING             = "DEPLOYING"
	DEPLOY_STATUS_FAIL_ON_LICENSE_ERROR = "FAIL_ON_LICENSE_ERROR"

	UNDEPLOY_STATUS_UNDEPLOYING = "UNDEPLOYING"
	UNDEPLOY_STATUS_SUCCESS     = DEPLOY_STATUS_SUCCESS
	UNDEPLOY_STATUS_FAIL        = DEPLOY_STATUS_FAIL
)

const (
	Artifact_Rt_Started  RuntimeState = "STARTED" // deployed in runtime
	Artifact_Rt_Error    RuntimeState = "ERROR"
	Artifact_Rt_Starting RuntimeState = "STARTING"
)

const (
	Artifact_Type_Iflow        ArtifactType = "Integration Flow"
	Artifact_Type_Sc           ArtifactType = "Script Collection"
	Artifact_Type_ValueMapping ArtifactType = "Value Mapping"
	Artifact_Type_MsgMapping   ArtifactType = "Message Mapping"
	Artifact_Type_DataType     ArtifactType = "Data Type"
	Artifact_Type_MsgType      ArtifactType = "Message Type"
	Artifact_Type_FaultMsgType ArtifactType = "Fault Message Type"
	Artifact_Type_SvcInterface ArtifactType = "Service Interface"
)

// ArtifactTypeToNavProperty maps artifact types to their CPI OData Navigation Property / EntitySet name.
// Used for Package Artifacts query, Direct Artifact query, and Download.
var ArtifactTypeToNavProperty = map[ArtifactType]string{
	Artifact_Type_Iflow:        "IntegrationDesigntimeArtifacts",
	Artifact_Type_Sc:           "ScriptCollectionDesigntimeArtifacts",
	Artifact_Type_ValueMapping: "ValueMappingDesigntimeArtifacts",
	Artifact_Type_MsgMapping:   "MessageMappingDesigntimeArtifacts",
	Artifact_Type_DataType:     "DataTypeDesigntimeArtifacts",
	Artifact_Type_MsgType:      "MessageTypeDesigntimeArtifacts",
	Artifact_Type_FaultMsgType: "FaultMessageTypeDesigntimeArtifacts",
	Artifact_Type_SvcInterface: "ServiceInterfaceDesigntimeArtifacts",
}

// ArtifactTypeToDeployEndpoint maps deployable artifact types to their CPI deploy endpoint.
// If a type is NOT in this map, it is not deployable.
var ArtifactTypeToDeployEndpoint = map[ArtifactType]string{
	Artifact_Type_Iflow:        "DeployIntegrationDesigntimeArtifact",
	Artifact_Type_Sc:           "DeployScriptCollectionDesigntimeArtifact",
	Artifact_Type_ValueMapping: "DeployValueMappingDesigntimeArtifact",
	Artifact_Type_MsgMapping:   "DeployMessageMappingDesigntimeArtifact",
}

// AllArtifactTypes returns all supported artifact types in a stable order for parallel queries.
func AllArtifactTypes() []ArtifactType {
	return []ArtifactType{
		Artifact_Type_Iflow, Artifact_Type_Sc, Artifact_Type_ValueMapping,
		Artifact_Type_MsgMapping, Artifact_Type_DataType, Artifact_Type_MsgType,
		Artifact_Type_FaultMsgType, Artifact_Type_SvcInterface,
	}
}

// IsDeployable returns true if the artifact type has a deploy endpoint.
func IsDeployable(t ArtifactType) bool {
	_, ok := ArtifactTypeToDeployEndpoint[t]
	return ok
}

const (
	Job_Type_Import = "Import"
	Job_Type_Deploy = "Deploy"
)
const (
	Step_Type_Import   = "Import"
	Step_Type_Deploy   = "Deploy"
	Step_Type_Undeploy = "Undeploy"
)

// HTTP request timeout constants — used by pkg/tms, pkg/cpi, pkg/xsuaa
const (
	DefaultRequestTimeout = 30 * time.Second  // GET 请求（轻量）
	LongRequestTimeout    = 120 * time.Second // GET 请求（大数据量，如日志、文件下载）
	ImportTimeout         = 60 * time.Second  // POST/DELETE 请求（Import、Deploy、Upload、Undeploy）
)

// --- Version Compare ---

// SnapshotStatus represents the state of a VersionCompareSnapshot record.
type SnapshotStatus string

const (
	SnapshotStatusRunning   SnapshotStatus = "running"
	SnapshotStatusCompleted SnapshotStatus = "completed"
	SnapshotStatusFailed    SnapshotStatus = "failed"
	SnapshotStatusNone      SnapshotStatus = "none" // virtual: no DB record exists
)

// TriggerStatus represents the outcome of a TriggerVersionCompare call.
type TriggerStatus string

const (
	TriggerStatusRunning     TriggerStatus = "running"
	TriggerStatusRateLimited TriggerStatus = "rate_limited"
	TriggerStatusConflict    TriggerStatus = "conflict"
)
