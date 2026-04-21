package service

// tms_service.go — TMS client factory.
//
// TmsClientFunc is the single dependency type for TMS access across the service
// and handler layers.  Call it to obtain a ready TransportService; it resolves
// CentralTmsContext from the DB and OAuth credentials from the provider
// Destination Service on each invocation.
//
// Production: use NewTmsFactory to build the func at startup.
// Tests: pass a function literal that returns a mock TransportService.
//
// loadTmsContext and buildTmsClient are unexported package helpers shared by
// NewTmsFactory and tms_node_registrar.go (which needs *tms.TmsClient for
// v1 node-management methods not exposed on TransportService).

import (
	"context"
	"fmt"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/tms"

	"gorm.io/gorm"
)

// TmsClientFunc resolves and returns a ready-to-use TransportService.
// Production value built by NewTmsFactory; test value is a literal returning a mock.
type TmsClientFunc func(ctx context.Context) (TransportService, error)

// NewTmsFactory returns a TmsClientFunc that loads CentralTmsContext from db,
// resolves OAuth credentials via providerDest, and constructs a *tms.TmsClient.
func NewTmsFactory(database *gorm.DB, providerDest *cf.DestinationServiceClient) TmsClientFunc {
	return func(ctx context.Context) (TransportService, error) {
		tmsCtx, err := loadTmsContext(database)
		if err != nil {
			return nil, err
		}
		return buildTmsClient(ctx, tmsCtx, providerDest)
	}
}

// loadTmsContext loads the single CentralTmsContext record from the DB.
// Used by NewTmsFactory and tms_node_registrar.go.
func loadTmsContext(database *gorm.DB) (*db.CentralTmsContext, error) {
	var tmsCtx db.CentralTmsContext
	if err := database.First(&tmsCtx).Error; err != nil {
		return nil, fmt.Errorf("central TMS context not configured: %w", err)
	}
	return &tmsCtx, nil
}

// buildTmsClient constructs a *tms.TmsClient from CentralTmsContext + ProviderDest.
// Used by NewTmsFactory and tms_node_registrar.go.
func buildTmsClient(ctx context.Context, tmsCtx *db.CentralTmsContext, providerDest *cf.DestinationServiceClient) (*tms.TmsClient, error) {
	if providerDest == nil {
		return nil, fmt.Errorf("ProviderDest not injected; cannot resolve TMS credentials")
	}
	dest, err := providerDest.GetDestination(ctx, tmsCtx.TmsApiDestinationName)
	if err != nil {
		return nil, fmt.Errorf("get TMS API destination %q: %w", tmsCtx.TmsApiDestinationName, err)
	}
	if dest == nil {
		return nil, fmt.Errorf("TMS API destination %q not found in provider Destination Service", tmsCtx.TmsApiDestinationName)
	}
	if dest.ClientId == "" || dest.ClientSecret == "" || dest.TokenServiceURL == "" {
		return nil, fmt.Errorf("TMS API destination %q is missing OAuth credentials (clientId/clientSecret/tokenServiceURL)", tmsCtx.TmsApiDestinationName)
	}

	return tms.NewTmsClient(ctx, dest.URL, dest.TokenServiceURL, dest.ClientId, dest.ClientSecret)
}
