package service

import (
	"context"
	"fmt"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
)

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
	configs, _ := db.GetAllIntegrationConfigs(s.DB)
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

// PersistResults saves check results to the database.
func (s *Service) PersistConnectivityResults(results []ConnectivityStatus) error {
	now := time.Now()
	var dbResults []db.ConnectivityCheckResult
	for _, r := range results {
		dbResults = append(dbResults, db.ConnectivityCheckResult{
			Type: r.Type, Name: r.Name, Status: r.Status, Message: r.Message, CheckedAt: now,
		})
	}
	return db.UpsertConnectivityResults(s.DB, dbResults)
}

// PersistConnectivityResult saves a single check result.
func (s *Service) PersistConnectivityResult(r ConnectivityStatus) error {
	return db.UpsertConnectivityResult(s.DB, db.ConnectivityCheckResult{
		Type: r.Type, Name: r.Name, Status: r.Status, Message: r.Message, CheckedAt: time.Now(),
	})
}

// GetLastConnectivityReport returns the cached results from the database.
func (s *Service) GetLastConnectivityReport() (*ConnectivityReport, error) {
	dbResults, err := db.GetAllConnectivityResults(s.DB)
	if err != nil || len(dbResults) == 0 {
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
