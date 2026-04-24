package db

import "gorm.io/gorm"

// IntegrationConfig maps singleton integration types to BTP Destination names.
// Integration types are pre-seeded at startup; admins can only update DestinationName and Enabled.
type IntegrationConfig struct {
	gorm.Model
	Type            string `gorm:"uniqueIndex" json:"type"`
	DestinationName string `json:"destinationName"`
	Enabled         bool   `json:"enabled" gorm:"default:false"`
	Description     string `json:"description"`
}

// predefined integration types with default destination names
var integrationSeeds = []IntegrationConfig{
	{Type: "jira", DestinationName: "cpi-delivery-jira", Description: "JIRA issue tracking integration"},
	{Type: "smtp", DestinationName: "cpi-delivery-smtp", Description: "Email notification via SMTP"},
	{Type: "github", DestinationName: "cpi-delivery-github", Description: "GitHub code sync integration"},
}

// SeedIntegrationConfigs ensures all predefined integration types exist in the database.
// Existing records are not overwritten — only missing types are inserted.
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

// GetIntegrationConfig retrieves an integration config by type.
func GetIntegrationConfig(db *gorm.DB, integrationType string) (*IntegrationConfig, error) {
	var config IntegrationConfig
	if err := db.Where("type = ?", integrationType).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// GetAllIntegrationConfigs retrieves all integration configs.
func GetAllIntegrationConfigs(db *gorm.DB) ([]IntegrationConfig, error) {
	var configs []IntegrationConfig
	if err := db.Find(&configs).Error; err != nil {
		return nil, err
	}
	return configs, nil
}

// UpdateIntegrationConfig updates DestinationName, Enabled, and Description for a given type.
func UpdateIntegrationConfig(db *gorm.DB, integrationType string, destName string, enabled bool, description string) (*IntegrationConfig, error) {
	var config IntegrationConfig
	if err := db.Where("type = ?", integrationType).First(&config).Error; err != nil {
		return nil, err
	}
	config.DestinationName = destName
	config.Enabled = enabled
	config.Description = description
	if err := db.Save(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}
