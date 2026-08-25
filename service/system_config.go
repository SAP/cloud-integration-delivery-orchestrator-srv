package service

import (
	"context"
	"fmt"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/env"

	"gorm.io/gorm/clause"
)

// --- Integration Config CRUD ---

// GetIntegrationConfig retrieves an integration config by type.
func (s *Service) GetIntegrationConfig(integrationType string) (*db.IntegrationConfig, error) {
	var config db.IntegrationConfig
	if err := s.DB.Where("type = ?", integrationType).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// GetAllIntegrationConfigs retrieves all integration configs.
func (s *Service) GetAllIntegrationConfigs() ([]db.IntegrationConfig, error) {
	var configs []db.IntegrationConfig
	if err := s.DB.Find(&configs).Error; err != nil {
		return nil, err
	}
	return configs, nil
}

// UpdateIntegrationConfig updates DestinationName, Enabled, and Description for a given type.
func (s *Service) UpdateIntegrationConfig(integrationType string, destName string, enabled bool, description string) (*db.IntegrationConfig, error) {
	var config db.IntegrationConfig
	if err := s.DB.Where("type = ?", integrationType).First(&config).Error; err != nil {
		return nil, err
	}
	config.DestinationName = destName
	config.Enabled = enabled
	config.Description = description
	if err := s.DB.Save(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// --- Connectivity Check ---

// ConnectivityStatus represents the check result of a single external dependency.
type ConnectivityStatus struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ConnectivityReport is a collection of check results with a timestamp.
type ConnectivityReport struct {
	CheckedAt time.Time            `json:"checkedAt"`
	Results   []ConnectivityStatus `json:"results"`
}

// CheckDatabase verifies database connectivity via SQL ping.
func (s *Service) CheckDatabase() ConnectivityStatus {
	sqlDB, err := s.DB.DB()
	if err != nil {
		return ConnectivityStatus{Name: "Database", Type: "database", Status: "error", Message: err.Error()}
	}
	if err := sqlDB.Ping(); err != nil {
		return ConnectivityStatus{Name: "Database", Type: "database", Status: "error", Message: err.Error()}
	}
	return ConnectivityStatus{Name: "Database", Type: "database", Status: "ok"}
}

// CheckTMS verifies TMS API connectivity by calling GetNodes.
func (s *Service) CheckTMS(ctx context.Context) ConnectivityStatus {
	tmsClient, err := s.TmsSvc(ctx)
	if err != nil {
		return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()}
	}
	if _, err = tmsClient.GetNodes(ctx); err != nil {
		return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()}
	}
	now := time.Now()
	s.DB.Model(&db.CentralTmsContext{}).Where("1 = 1").Update("last_validated_at", now)
	return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "ok"}
}

// CheckTenants verifies all CPI tenant destinations.
func (s *Service) CheckTenants(ctx context.Context) []ConnectivityStatus {
	var tenants []db.CpiTenant
	s.DB.Find(&tenants)

	var results []ConnectivityStatus
	for _, t := range tenants {
		results = append(results, s.checkDestinationAuth(ctx, t.Name, "cpi_tenant", t.PirApiDestinationName))
	}
	return results
}

// CheckIntegration verifies a single integration's destination connectivity.
func (s *Service) CheckIntegration(ctx context.Context, cfg db.IntegrationConfig) ConnectivityStatus {
	return s.checkDestinationAuth(ctx, cfg.Type, "integration", cfg.DestinationName)
}

// CheckAllIntegrations verifies all integration destinations.
func (s *Service) CheckAllIntegrations(ctx context.Context) []ConnectivityStatus {
	configs, _ := s.GetAllIntegrationConfigs()
	var results []ConnectivityStatus
	for _, cfg := range configs {
		results = append(results, s.CheckIntegration(ctx, cfg))
	}
	return results
}

// CheckAll runs full connectivity check across all dependencies.
func (s *Service) CheckAll(ctx context.Context) ConnectivityReport {
	var results []ConnectivityStatus
	results = append(results, s.CheckDatabase())
	results = append(results, s.CheckTMS(ctx))
	results = append(results, s.CheckTenants(ctx)...)
	results = append(results, s.CheckAllIntegrations(ctx)...)
	return ConnectivityReport{CheckedAt: time.Now(), Results: results}
}

// PersistConnectivityResults saves check results to the database.
func (s *Service) PersistConnectivityResults(results []ConnectivityStatus) error {
	now := time.Now()
	var dbResults []db.ConnectivityCheckResult
	for _, r := range results {
		dbResults = append(dbResults, db.ConnectivityCheckResult{
			Type: r.Type, Name: r.Name, Status: r.Status, Message: r.Message, CheckedAt: now,
		})
	}
	if len(dbResults) == 0 {
		return nil
	}
	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "type"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "message", "checked_at"}),
	}).Create(&dbResults).Error
}

// PersistConnectivityResult saves a single check result.
func (s *Service) PersistConnectivityResult(r ConnectivityStatus) error {
	record := db.ConnectivityCheckResult{
		Type: r.Type, Name: r.Name, Status: r.Status, Message: r.Message, CheckedAt: time.Now(),
	}
	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "type"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "message", "checked_at"}),
	}).Create(&record).Error
}

// GetLastConnectivityReport returns the cached results from the database.
func (s *Service) GetLastConnectivityReport() (*ConnectivityReport, error) {
	var dbResults []db.ConnectivityCheckResult
	if err := s.DB.Find(&dbResults).Error; err != nil {
		return nil, err
	}
	if len(dbResults) == 0 {
		return nil, fmt.Errorf("no connectivity check has been performed yet")
	}

	var results []ConnectivityStatus
	var latestAt time.Time
	for _, r := range dbResults {
		results = append(results, ConnectivityStatus{
			Name: r.Name, Type: r.Type, Status: r.Status, Message: r.Message,
		})
		if r.CheckedAt.After(latestAt) {
			latestAt = r.CheckedAt
		}
	}
	return &ConnectivityReport{CheckedAt: latestAt, Results: results}, nil
}

// --- internal helper ---

func (s *Service) checkDestinationAuth(ctx context.Context, name, typeName, destName string) ConnectivityStatus {
	if destName == "" {
		return ConnectivityStatus{
			Name: name, Type: typeName, Status: "error",
			Message: "no destination configured",
		}
	}

	dest, err := s.ProviderDest.GetDestination(ctx, destName)
	if err != nil {
		return ConnectivityStatus{
			Name: name, Type: typeName, Status: "error",
			Message: fmt.Sprintf("destination '%s' not found: %s", destName, err),
		}
	}
	if dest == nil {
		return ConnectivityStatus{
			Name: name, Type: typeName, Status: "error",
			Message: fmt.Sprintf("destination '%s' not found", destName),
		}
	}

	if _, err := env.NewClient(ctx, dest.ClientId, dest.ClientSecret, dest.TokenServiceURL, dest.URL); err != nil {
		return ConnectivityStatus{
			Name: name, Type: typeName, Status: "error",
			Message: fmt.Sprintf("destination '%s' found but token fetch failed: %s", destName, err),
		}
	}

	return ConnectivityStatus{
		Name: name, Type: typeName, Status: "ok",
		Message: fmt.Sprintf("destination '%s' resolved and authenticated", destName),
	}
}
