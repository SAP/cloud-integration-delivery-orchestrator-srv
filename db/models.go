package db

import (
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Job struct {
	gorm.Model
	Name        string
	Description string
	Status      string
	Type        string
}

type ImportStep struct {
	gorm.Model        `mapstructure:",squash"`
	JobId             uint
	Sequence          uint
	Status            string
	TransportNodeId   uint
	TransportNodeName string
	TransportRequests pq.Int32Array `gorm:"type:integer[]"`
	ActionId          uint
}

type DeployStep struct {
	gorm.Model       `mapstructure:",squash"`
	JobId            uint
	Sequence         uint
	Status           string
	Endpoint         string
	PackageId        string
	ArtifactIds      pq.StringArray `gorm:"type:varchar[]"`
	ArtifactTypes    pq.StringArray `gorm:"type:varchar[]"`
	ArtifactVersions pq.StringArray `gorm:"type:varchar[]"`
	TaskIds          pq.StringArray `gorm:"type:varchar[]"`
	TaskStatuses     pq.StringArray `gorm:"type:varchar[]"`
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
