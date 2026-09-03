package service

import (
	"context"
	"fmt"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
)

// --- Connectivity Check ---

// ConnectivityStatus represents the check result of a single external dependency.
type ConnectivityStatus struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
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
