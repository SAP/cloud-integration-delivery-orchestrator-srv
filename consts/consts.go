package consts

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
	Artifact_Rt_Started  RuntimeState = "STARTED"
	Artifact_Rt_Error    RuntimeState = "ERROR"
	Artifact_Rt_Starting RuntimeState = "STARTING"
)

const (
	Artifact_Type_Iflow ArtifactType = "Integration Flow"
	Artifact_Type_Sc    ArtifactType = "Script Collection"
)

const (
	Job_Type_Import = "Import"
	Job_Type_Deploy = "Deploy"
)
const (
	Step_Type_Import   = "Import"
	Step_Type_Deploy   = "Deploy"
	Step_Type_Undeploy = "Undeploy"
)
