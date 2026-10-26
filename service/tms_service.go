package service

// tms_service.go — TMS client factory.
//
// TmsClientFunc is the single dependency type for TMS access across the service
// and handler layers.  Call it to obtain a ready TransportService; it resolves
// CentralTmsContext from the DB and OAuth credentials from the provider
// Destination Service on first invocation, then reuses the same client instance.
//
// Production: use NewTmsFactory to build the func at startup.
// Tests: pass a function literal that returns a mock TransportService.
//
// loadTmsContext and buildTmsClient are unexported package helpers shared by
// NewTmsFactory and tms_node_registrar.go (which needs *tms.TmsClient for
// v1 node-management methods not exposed on TransportService).

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/tms"

	"gorm.io/gorm"
)

// TmsClientFunc resolves and returns a ready-to-use TransportService.
// Production value built by NewTmsFactory; test value is a literal returning a mock.
type TmsClientFunc func(ctx context.Context) (TransportService, error)

// NewTmsFactory returns a TmsClientFunc that lazily creates a single TMS client
// and reuses it for all subsequent calls. Token refresh is handled internally
// by the underlying HttpClient.
func NewTmsFactory(database *gorm.DB, providerDest *cf.DestinationServiceClient) TmsClientFunc {
	var (
		mu     sync.Mutex
		client TransportService
	)
	return func(ctx context.Context) (TransportService, error) {
		mu.Lock()
		defer mu.Unlock()
		if client != nil {
			return client, nil
		}
		tmsCtx, err := loadTmsContext(database)
		if err != nil {
			return nil, err
		}
		c, err := buildTmsClient(ctx, tmsCtx, providerDest)
		if err != nil {
			return nil, err
		}
		client = c
		return client, nil
	}
}

// loadTmsContext loads the single CentralTmsContext record from the DB.
func loadTmsContext(database *gorm.DB) (*db.CentralTmsContext, error) {
	var tmsCtx db.CentralTmsContext
	if err := database.First(&tmsCtx).Error; err != nil {
		return nil, fmt.Errorf("central TMS context not configured: %w", err)
	}
	return &tmsCtx, nil
}

// LoadCentralTmsContext is the exported wrapper for loadTmsContext.
func (s *Service) LoadCentralTmsContext() (*db.CentralTmsContext, error) {
	return loadTmsContext(s.DB)
}

// ErrTmsBootstrapInProgress is returned by UpsertCentralTmsContext when the
// destination name is changed while one or more tenants have TMS extension
// bootstrap running.
var ErrTmsBootstrapInProgress = errors.New("TMS extension bootstrap is in progress")

// UpsertCentralTmsContextResult holds the saved context and how many tenants
// were invalidated by Rule 4.
type UpsertCentralTmsContextResult struct {
	Context         db.CentralTmsContext
	AffectedTenants int64
}

// UpsertCentralTmsContext creates or fully replaces the CentralTmsContext.
// v1 supports a single record; subsequent calls overwrite it in place.
//
// Rule 4 (RFC 032 §5.4): if TmsApiDestinationName is changed and tenants exist
// with TmsExtensionState=bootstrapped, they are invalidated to not_started
// (their subscriber TransportManagementService destination holds stale credentials).
// If any tenant is in TmsExtensionState=bootstrapping, returns ErrTmsBootstrapInProgress.
func (s *Service) UpsertCentralTmsContext(input db.CentralTmsContext) (*UpsertCentralTmsContextResult, error) {
	var existing db.CentralTmsContext
	err := s.DB.First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("load existing context: %w", err)
	}

	// Carry existing ID so Save does UPDATE instead of INSERT.
	if existing.ID != 0 {
		input.ID = existing.ID
	}

	destNameChanged := existing.ID != 0 && existing.TmsApiDestinationName != input.TmsApiDestinationName

	var affectedCount int64
	if destNameChanged {
		// Rule 4 requires atomicity: reject if bootstrapping, invalidate bootstrapped, save context.
		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			var bootstrappingCount int64
			if err := tx.Model(&db.CpiTenant{}).
				Where("tms_extension_state = ?", lifecycle.TmsBootstrapping).
				Count(&bootstrappingCount).Error; err != nil {
				return fmt.Errorf("rule 4: count bootstrapping tenants: %w", err)
			}
			if bootstrappingCount > 0 {
				return fmt.Errorf("%d tenant(s) have TMS extension bootstrap running; wait for completion before changing Central TMS destination: %w",
					bootstrappingCount, ErrTmsBootstrapInProgress)
			}

			// Reset to not_started and clear node fields — Central TMS destination change
			// may point to a different TMS system where old node IDs/names map to
			// different tenants. Only reset fields affected by Central TMS change;
			// CAS/CloudIntegration fields are unrelated and will be re-verified during re-apply.
			result := tx.Model(&db.CpiTenant{}).
				Where("tms_extension_state = ?", lifecycle.TmsBootstrapped).
				Updates(map[string]any{
					"tms_extension_state":              lifecycle.TmsNotStarted,
					"tms_blocking_reason":              lifecycle.TmsReasonCentralTmsChanged,
					"tms_source_node_name":             "",
					"tms_source_node_id":               0,
					"transport_management_dest_status": lifecycle.PrereqMissing,
				})
			if result.Error != nil {
				return fmt.Errorf("rule 4: invalidate tenants: %w", result.Error)
			}
			affectedCount = result.RowsAffected

			return tx.Save(&input).Error
		}); err != nil {
			return nil, err
		}
	} else {
		if err := s.DB.Save(&input).Error; err != nil {
			return nil, fmt.Errorf("save context: %w", err)
		}
	}

	return &UpsertCentralTmsContextResult{
		Context:         input,
		AffectedTenants: affectedCount,
	}, nil
}

// buildTmsClient constructs a *tms.TmsClient from CentralTmsContext + ProviderDest.
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
