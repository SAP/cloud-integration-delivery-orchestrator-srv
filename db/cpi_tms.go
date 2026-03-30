package db

import (
	. "mmt-delivery/consts"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

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
	Name                     string `gorm:"uniqueIndex,where:deleted_at IS NULL"` // grom tag for soft delete issue. cpi-mmt-dev, cpi-ci, may use cpi tenant domain
	CreatedBy                string
	UpdatedBy                string
	TransportNodeID          uint        //TMS Node ID
	TransportNodeName        string      // TMS Node Name, for easier query
	TransportNodeDescription string      // TMS Node Description
	CpiEndpoint              ApiEndpoint `gorm:"serializer:json"`
	Group                    string      // prod(cpi-prod-01, cpi-prod-02, ...), ctest(cpi-ctest, cpi-ctest-01), ep(preprod-ep), ...
}

type UaaClaims struct {
	UserName string   `json:"user_name"`
	Scope    []string `json:"scope"`
	jwt.RegisteredClaims
	Origin string `json:"origin"` //maco.accounts400.ondemand.com
	UserID string `json:"user_id"`
	ZoneID string `json:"zid"`
}
