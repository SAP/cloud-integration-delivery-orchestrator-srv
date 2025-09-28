package db

import (
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Job struct {
	gorm.Model
	Name        string
	Description string
	Status      string
	Type        string

	CreatedBy   string
	UpdatedBy   string
	TriggeredBy string
}

type ImportStep struct {
	gorm.Model
	JobId             uint
	Sequence          uint
	Status            string
	TransportNodeId   uint
	TransportNodeName string
	ActionId          uint
	Type              string //Step type: Import, Deploy, Undeploy
	// version 2.0.0. as an replacement of TransportRequests/Descriptions
	TransportRequests_V2 []TransportRequest `gorm:"serializer:json"`
	UpdatedBy            string
	TriggeredBy          string
	TriggeredAt          time.Time
	EndedAt              time.Time
}

type DeployStep struct {
	gorm.Model
	JobId    uint
	Sequence uint
	Status   string
	Endpoint string
	Type     string // Deploy/Undeploy

	Artifacts []Artifact `gorm:"serializer:json"` // version 2.0.0. as an replacement of ArtifactIds/Types/Versions...

	UpdatedBy   string
	TriggeredBy string
	TriggeredAt time.Time
	EndedAt     time.Time
}

// execution log of a job
type ExecutionLog struct {
	gorm.Model
	JobId    uint
	StepId   uint
	Sequence uint
	StepType string
	Log      string
}

// Artifact needed to be deployed
type Artifact struct {
	Id          string // artifact id
	Version     string
	PackageId   string
	Name        string
	Type        string // iflow, scriptCollection
	Description string
	CreatedBy   string
	CreatedAt   string
	ModifiedBy  string
	ModifiedAt  string
	Status      string // deploy stask status
	TaskId      string // task id
}

type TransportRequest struct {
	ID          int //tr number
	Description string
	Status      string
}

// Parsed from yaml file
type TransportPlan struct {
	gorm.Model
	CreatedBy string
	UpdatedBy string

	Name        string
	Description string

	Artifacts               []Artifact         `gorm:"serializer:json"`
	TransportRequests       []TransportRequest `gorm:"serializer:json"` // transport request numbers
	ImportJobId             uint               // import job id in table Job
	DeployJobId             uint               // deploy job id in table Job
	VerifyTransportRequests string             // verify tr numbers exist in tms nodes. Pass/Fail
	VerifyArtifacts         string             // verify artifacts exist in cpi tenant. Pass/Fail
	ImportJobStatus         string             // update from import job status
	DeployJobStatus         string             // update from deploy job status

	TransportGroupID   int    // transport group id in Database
	TransportGroupName string // transport group name
}

type TransportNode struct {
	ID                   uint   `json:"id"`
	Description          string `json:"description"`
	Name                 string `json:"name"`
	UploadAllowed        bool   `json:"uploadAllowed"`
	NotificationEnabled  bool   `json:"notificationEnabled"`
	ForwardMode          string `json:"forwardMode"`
	ImportDisabled       bool   `json:"importDisabled"`
	ImportDisabledReason string `json:"importDisabledReason"`
	Targets              []struct {
		ID              int    `json:"id"`
		ContentType     string `json:"contentType"`
		DestinationName string `json:"destinationName"`
		ImportOptions   struct {
			Strategy string `json:"strategy"`
		} `json:"importOptions"`
	} `json:"targets"`
	Virtual bool `json:"virtual"`
}

type TransportRoute struct {
	ID           uint   `json:"id"`
	Description  string `json:"description"`
	Name         string `json:"name"`
	SourceNodeID uint   `json:"sourceNodeId"`
	TargetNodeID uint   `json:"targetNodeId"`
}

// import and deploy group
type TransportGroup struct {
	gorm.Model
	CreatedBy string
	UpdatedBy string

	Name            string          //group name
	Description     string          //group description
	TransportNodes  []TransportNode `gorm:"serializer:json"` // tms node ids
	DeployEndpoints pq.StringArray  `gorm:"type:varchar[]"`  // CPI deploy endpoints
}

// ApiEndpoint mirrors the TS interface ApiEndpoint
type ApiEndpoint struct {
	gorm.Model
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// bind cpi tenant with tms node
type CpiTenant struct {
	gorm.Model
	Name          string `gorm:"uniqueIndex,where:deleted_at IS NULL"` // grom tag for soft delete issue. cpi-mmt-dev, cpi-ci, may use cpi tenant domain
	CreatedBy     string
	UpdatedBy     string
	TransportNode TransportNode `gorm:"serializer:json"`
	CpiEndpoint   ApiEndpoint   `gorm:"serializer:json"`
}

type DeliveryRule struct {
	gorm.Model
	Name           string
	VersionPattern string

	// Associations to CpiTenant
	IncludedTenants []CpiTenant `gorm:"serializer:json;"` // included CPI tenants
	ExcludedTenants []CpiTenant `gorm:"serializer:json;"` // excluded CPI tenants

	Active    bool
	CreatedBy string
	UpdatedBy string
}

type DeliveryRequest struct {
	gorm.Model
	Name string

	JiraLink string // related Jira ticket URL
	Status   string // pending, in-progress, completed, failed

	Artifacts []Artifact `gorm:"serializer:json"` // artifacts to deliver

	SourceTenantID *uint      // source cpi tenant id
	SourceTenant   *CpiTenant `gorm:"foreignKey:SourceTenantID"` // source CPI tenant

	DeliveryRuleID *uint
	DeliveryRule   *DeliveryRule `gorm:"foreignKey:DeliveryRuleID"` // FK association to delivery_rules table

	TargetNodes  []TransportNode  `gorm:"serializer:json"` // target transport nodes, derived from DeliveryRule
	TargetRoutes []TransportRoute `gorm:"serializer:json"` // derived transport routes, based on targetNodes and sourceNode

	CreatedBy string
	UpdatedBy string
}
