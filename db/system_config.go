package db

import (
	"time"

	"gorm.io/gorm"
)

// --- Integration Config ---

// IntegrationConfig maps singleton integration types to BTP Destination names.
type IntegrationConfig struct {
	gorm.Model
	Type            string `gorm:"uniqueIndex" json:"type"`
	DestinationName string `json:"destinationName"`
	Enabled         bool   `json:"enabled" gorm:"default:false"`
	Description     string `json:"description"`
}

var integrationSeeds = []IntegrationConfig{
	{Type: "jira", DestinationName: "cpi-delivery-jira", Description: "JIRA issue tracking integration"},
	{Type: "smtp", DestinationName: "cpi-delivery-smtp", Description: "Email notification via SMTP"},
	{Type: "github", DestinationName: "cpi-delivery-github", Description: "GitHub code sync integration"},
}

// SeedIntegrationConfigs ensures all predefined integration types exist in the database.
// Called during DB initialization (conn.go).
func SeedIntegrationConfigs(db *gorm.DB) error {
	for _, seed := range integrationSeeds {
		var existing IntegrationConfig
		result := db.Where("type = ?", seed.Type).First(&existing)
		if result.Error == gorm.ErrRecordNotFound {
			if err := db.Create(&seed).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// --- Connectivity Check Result ---

// ConnectivityCheckResult stores per-dependency connectivity status.
// Composite primary key: (Type, Name).
type ConnectivityCheckResult struct {
	Type      string    `gorm:"primaryKey" json:"type"`
	Name      string    `gorm:"primaryKey" json:"name"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}
