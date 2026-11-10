package service

import (
	"context"
	"fmt"

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

// TenantConnectivity is a tenant's check result in two tiers: Basic (CPI, a
// prerequisite for every onboarded tenant) and Extensions (capabilities present
// only once enabled — currently "cas", later e.g. "github_branch"). Extensions
// contains only the capabilities this tenant actually has, so the frontend can
// render them as a secondary tier without a dedicated "warn" status.
type TenantConnectivity struct {
	TenantID   uint                          `json:"tenantId"`
	TenantName string                        `json:"tenantName"`
	Basic      ConnectivityStatus            `json:"basic"`
	Extensions map[string]ConnectivityStatus `json:"extensions,omitempty"`
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
// client. It is an online probe whose result is returned to the caller in real
// time; it deliberately does NOT write last_validated_at. That field means
// "when the active config was validated at Reconfigure time" and is written
// (version-consistent) only by the Reconfigure commit — letting an ad-hoc
// connection check stamp it would make the field diverge between memory and DB
// across restarts (R3).
func (s *Service) CheckTMS(ctx context.Context) ConnectivityStatus {
	tmsClient, err := s.TMS.Get(ctx)
	if err != nil {
		return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()}
	}
	if _, err = tmsClient.GetNodes(ctx); err != nil {
		return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "error", Message: err.Error()}
	}
	return ConnectivityStatus{Name: "TMS", Type: "tms", Status: "ok"}
}

// CheckTenants verifies connectivity for every tenant in two tiers: Basic is
// the tenant's CPI client (resolved via the manager and authenticated, i.e. the
// same client business code uses); Extensions holds capability checks that only
// apply once provisioned — currently CAS, included only when the tenant has a
// CAS engine destination.
func (s *Service) CheckTenants(ctx context.Context) []TenantConnectivity {
	var tenants []db.CpiTenant
	s.DB.Find(&tenants)

	results := make([]TenantConnectivity, 0, len(tenants))
	for _, t := range tenants {
		tc := TenantConnectivity{
			TenantID:   t.ID,
			TenantName: t.Name,
			Basic:      s.checkTenantCPI(ctx, t),
		}
		if t.CasEngineDestinationName != "" {
			tc.Extensions = map[string]ConnectivityStatus{"cas": s.checkTenantCAS(ctx, t)}
		}
		results = append(results, tc)
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
