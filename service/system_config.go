package service

import (
	"context"
	"fmt"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
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

// CheckTMS verifies TMS API connectivity by calling GetNodes on the active
// client. last_validated_at is stamped only onto the config version that was
// checked: if a Reconfigure advances the generation during the check, the
// version-scoped UPDATE matches nothing and the stale result is not recorded
// against the new config (§7.2.4).
func (s *Service) CheckTMS(ctx context.Context) ConnectivityStatus {
	var cfg db.CentralTmsContext
	if err := s.DB.First(&cfg).Error; err != nil {
		return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: "central TMS not configured"}
	}
	checkedGen := cfg.Generation

	tmsClient, err := s.TMS.Get(ctx)
	if err != nil {
		return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()}
	}
	if _, err = tmsClient.GetNodes(ctx); err != nil {
		return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()}
	}
	now := time.Now()
	s.DB.Model(&db.CentralTmsContext{}).Where("generation = ?", checkedGen).Update("last_validated_at", now)
	return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "ok"}
}

// CheckTenants verifies basic connectivity for every tenant: it resolves the
// tenant's CPI client through the CPI manager (the same client business code
// uses) and authenticates it. This is the "basic" tier — CPI is a prerequisite
// for every onboarded tenant.
func (s *Service) CheckTenants(ctx context.Context) []ConnectivityStatus {
	var tenants []db.CpiTenant
	s.DB.Find(&tenants)

	var results []ConnectivityStatus
	for _, t := range tenants {
		results = append(results, s.checkTenantCPI(ctx, t))
	}
	return results
}

// CheckCasTenants verifies CAS connectivity for tenants that have a CAS engine
// destination (i.e. the TMS extension provisioned one). Tenants without it are
// skipped — CAS is an extension capability, not part of basic readiness. Uses
// the CAS manager's client (business-same) and authenticates it.
func (s *Service) CheckCasTenants(ctx context.Context) []ConnectivityStatus {
	var tenants []db.CpiTenant
	s.DB.Find(&tenants)

	var results []ConnectivityStatus
	for _, t := range tenants {
		if t.CasEngineDestinationName == "" {
			continue
		}
		results = append(results, s.checkTenantCAS(ctx, t))
	}
	return results
}

// --- internal helpers ---

// checkTenantCPI resolves the tenant's CPI client via the manager and
// authenticates it — proving the destination's credentials actually obtain a
// token, not merely that a client struct could be constructed.
func (s *Service) checkTenantCPI(ctx context.Context, t db.CpiTenant) ConnectivityStatus {
	cli, err := s.CPI.Get(ctx, t.ID)
	if err != nil {
		return ConnectivityStatus{Name: t.Name, Type: "cpi_tenant", Status: "error", Message: err.Error()}
	}
	if err := cli.Authenticate(ctx); err != nil {
		return ConnectivityStatus{Name: t.Name, Type: "cpi_tenant", Status: "error", Message: fmt.Sprintf("authentication failed: %s", err)}
	}
	return ConnectivityStatus{Name: t.Name, Type: "cpi_tenant", Status: "ok"}
}

// checkTenantCAS resolves the tenant's CAS client via the manager and
// authenticates it.
func (s *Service) checkTenantCAS(ctx context.Context, t db.CpiTenant) ConnectivityStatus {
	cli, err := s.CAS.Get(ctx, t.ID)
	if err != nil {
		return ConnectivityStatus{Name: t.Name, Type: "cas_tenant", Status: "error", Message: err.Error()}
	}
	if err := cli.Authenticate(ctx); err != nil {
		return ConnectivityStatus{Name: t.Name, Type: "cas_tenant", Status: "error", Message: fmt.Sprintf("authentication failed: %s", err)}
	}
	return ConnectivityStatus{Name: t.Name, Type: "cas_tenant", Status: "ok"}
}

// checkDestinationAuth resolves a destination by name and authenticates it by
// actually obtaining a token. Used by callers without a manager-cached client
// (e.g. Jira). CPI/CAS checks do NOT use this — they go through their managers
// to exercise the same client business code uses.
func (s *Service) checkDestinationAuth(ctx context.Context, name, typeName, destName string) ConnectivityStatus {
	if destName == "" {
		return ConnectivityStatus{Name: name, Type: typeName, Status: "error", Message: "no destination configured"}
	}
	dest, err := s.ProviderDest.GetDestination(ctx, destName)
	if err != nil {
		return ConnectivityStatus{Name: name, Type: typeName, Status: "error", Message: fmt.Sprintf("destination '%s' not found: %s", destName, err)}
	}
	if dest == nil {
		return ConnectivityStatus{Name: name, Type: typeName, Status: "error", Message: fmt.Sprintf("destination '%s' not found", destName)}
	}
	client, err := env.NewClient(ctx, dest.ClientId, dest.ClientSecret, dest.TokenServiceURL, dest.URL)
	if err != nil {
		return ConnectivityStatus{Name: name, Type: typeName, Status: "error", Message: fmt.Sprintf("destination '%s': %s", destName, err)}
	}
	if err := client.Authenticate(ctx); err != nil {
		return ConnectivityStatus{Name: name, Type: typeName, Status: "error", Message: fmt.Sprintf("destination '%s' authentication failed: %s", destName, err)}
	}
	return ConnectivityStatus{Name: name, Type: typeName, Status: "ok", Message: fmt.Sprintf("destination '%s' resolved and authenticated", destName)}
}
