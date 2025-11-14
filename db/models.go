package db

import (
	. "mmt-delivery/consts"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

// on artifact_tech_id:version can be deployed to *multiple* tenants, so it is better to seperate it into a new table!
// search by artifact_tech_id:version, no need to use ID.
type Artifact struct {
	gorm.Model

	TechID      string `gorm:"index:ux_artifact_tech_version,unique"` // artifact technical id
	Version     string `gorm:"index:ux_artifact_tech_version,unique"`
	Name        string
	PackageID   string       //package techical id
	Type        ArtifactType // iflow, scriptCollection
	Description string
	CreatedBy   string //TODO: may not need it. same above
	CreatedAt   string
	ModifiedBy  string
	ModifiedAt  string
	Status      string // deploy task status. TODO: may not need it. status will be controlled by ArtifactTenantOperation
	TaskId      string // task id. TODO: may not need it. same as above
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

type UaaClaims struct {
	UserName string   `json:"user_name"`
	Scope    []string `json:"scope"`
	jwt.RegisteredClaims
}
