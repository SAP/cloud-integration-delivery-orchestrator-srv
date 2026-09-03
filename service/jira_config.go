package service

import (
	"context"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
)

// GetJiraConfig returns the singleton JiraConfig row, or nil if not yet configured.
func (s *Service) GetJiraConfig() (*db.JiraConfig, error) {
	var cfg db.JiraConfig
	if err := s.DB.First(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// UpdateJiraConfig upserts the singleton JiraConfig row.
func (s *Service) UpdateJiraConfig(destName string, enabled bool) (*db.JiraConfig, error) {
	var cfg db.JiraConfig
	if err := s.DB.First(&cfg).Error; err != nil {
		// No row yet — create
		cfg = db.JiraConfig{DestinationName: destName, Enabled: enabled}
		if err := s.DB.Create(&cfg).Error; err != nil {
			return nil, err
		}
		return &cfg, nil
	}
	cfg.DestinationName = destName
	cfg.Enabled = enabled
	if err := s.DB.Save(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// TestJiraConnection verifies that the BTP Destination for Jira is reachable
// and can obtain an authenticated client. Returns a ConnectivityStatus.
func (s *Service) TestJiraConnection(ctx context.Context) ConnectivityStatus {
	cfg, err := s.GetJiraConfig()
	if err != nil {
		return ConnectivityStatus{Name: "jira", Type: "jira", Status: "error", Message: "jira config not found"}
	}
	return s.checkDestinationAuth(ctx, "jira", "jira", cfg.DestinationName)
}
