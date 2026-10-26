package db

import (
	"fmt"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/gorm"
)

// DataMigration tracks which one-time data migrations have been applied.
// Each migration is idempotent and runs in a single transaction.
type DataMigration struct {
	Name      string    `gorm:"primaryKey"`
	AppliedAt time.Time `gorm:"not null"`
}

type migrationEntry struct {
	Name string
	Fn   func(tx *gorm.DB) error
}

// registry lists all data migrations in execution order.
var registry = []migrationEntry{
	{"p0_tms_state_backfill", p0TmsStateBackfill},
	{"p0_job_layer_backfill", p0JobLayerBackfill},
}

// RunDataMigrations executes any pending data migrations.
// Must be called after AutoMigrate has created/updated all tables.
func RunDataMigrations(db *gorm.DB) error {
	for _, m := range registry {
		var existing DataMigration
		if err := db.Where("name = ?", m.Name).First(&existing).Error; err == nil {
			continue // already applied
		}
		env.Logger().Infow("running data migration", "name", m.Name)
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := m.Fn(tx); err != nil {
				return err
			}
			return tx.Create(&DataMigration{
				Name:      m.Name,
				AppliedAt: time.Now(),
			}).Error
		}); err != nil {
			return fmt.Errorf("data migration %q failed: %w", m.Name, err)
		}
		env.Logger().Infow("data migration applied", "name", m.Name)
	}
	return nil
}

// p0TmsStateBackfill populates TmsExtensionState from the legacy TmsNodeRegistrationStatus
// and merges not_ready → configured.
//
// Mapping (from RFC 032 06-p0 section 5.8):
//
//	LifecycleState=ready AND TmsNodeRegistrationStatus=ready  → TmsBootstrapped
//	TmsNodeRegistrationStatus=ready (lifecycle != ready)       → TmsNodeConfirmed
//	TmsNodeRegistrationStatus=registering                      → TmsNodeConfirmed
//	everything else                                            → TmsNotStarted (column default)
//	LifecycleState=not_ready                                   → configured
func p0TmsStateBackfill(tx *gorm.DB) error {
	// 1. ready lifecycle + ready node → bootstrapped
	if err := tx.Model(&CpiTenant{}).
		Where("lifecycle_state = ? AND tms_node_registration_status = ?",
			lifecycle.TenantReady, lifecycle.PrereqReady).
		Update("tms_extension_state", lifecycle.TmsBootstrapped).Error; err != nil {
		return fmt.Errorf("backfill TmsBootstrapped: %w", err)
	}

	// 2. node ready but lifecycle not ready → node_confirmed
	if err := tx.Model(&CpiTenant{}).
		Where("lifecycle_state != ? AND tms_node_registration_status = ?",
			lifecycle.TenantReady, lifecycle.PrereqReady).
		Update("tms_extension_state", lifecycle.TmsNodeConfirmed).Error; err != nil {
		return fmt.Errorf("backfill TmsNodeConfirmed: %w", err)
	}

	// 3. node registering → node_confirmed (node_pending removed)
	if err := tx.Model(&CpiTenant{}).
		Where("tms_node_registration_status = ?", lifecycle.PrereqRegistering).
		Update("tms_extension_state", lifecycle.TmsNodeConfirmed).Error; err != nil {
		return fmt.Errorf("backfill TmsNodeConfirmed (registering): %w", err)
	}

	// 4. everything else: column default is 'not_started', no action needed.

	// 5. Merge not_ready → configured (§2.1.1: state does not encode outcome).
	if err := tx.Model(&CpiTenant{}).
		Where("lifecycle_state = ?", "not_ready").
		Update("lifecycle_state", lifecycle.TenantConfigured).Error; err != nil {
		return fmt.Errorf("backfill merge not_ready: %w", err)
	}

	// 6. Merge bootstrap_failed → node_confirmed.
	if err := tx.Model(&CpiTenant{}).
		Where("tms_extension_state = ?", "bootstrap_failed").
		Update("tms_extension_state", lifecycle.TmsNodeConfirmed).Error; err != nil {
		return fmt.Errorf("backfill merge bootstrap_failed: %w", err)
	}

	// 7. Merge node_pending → node_confirmed.
	if err := tx.Model(&CpiTenant{}).
		Where("tms_extension_state = ?", "node_pending").
		Update("tms_extension_state", lifecycle.TmsNodeConfirmed).Error; err != nil {
		return fmt.Errorf("backfill merge node_pending: %w", err)
	}

	return nil
}

// p0JobLayerBackfill sets Layer=BASE on all legacy bootstrap jobs (Layer="").
// Pre-P0 jobs were combined BASE+TMS; going forward they map to BASE.
func p0JobLayerBackfill(tx *gorm.DB) error {
	if err := tx.Model(&TenantBootstrapJob{}).
		Where("layer = ? OR layer IS NULL", "").
		Update("layer", lifecycle.LayerBase).Error; err != nil {
		return fmt.Errorf("backfill job layer: %w", err)
	}
	return nil
}
