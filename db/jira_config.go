package db

import "gorm.io/gorm"

// JiraConfig stores the Jira integration settings (singleton row).
// The BTP Destination referenced by DestinationName provides the Jira base URL
// and BasicAuthentication credentials used by pkg/notify/jira.go.
type JiraConfig struct {
	gorm.Model
	DestinationName string `json:"destinationName"`
	Enabled         bool   `json:"enabled" gorm:"default:false"`
}
