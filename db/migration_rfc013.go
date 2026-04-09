package db

import (
	"gorm.io/gorm"
)

// PreAutoMigrate013 must be called BEFORE AutoMigrate when deploying RFC 013 changes.
//
// # Why this is needed
//
// RFC 013 adds SubaccountID to CpiTenant with a unique index.  GORM AutoMigrate
// would first ADD COLUMN (setting every existing row to ""), then try to CREATE
// UNIQUE INDEX — which fails immediately if multiple active tenants exist.
//
// This function detects that situation and pre-populates SubaccountID with
// distinct placeholder values ("PENDING_<id>") so AutoMigrate can create the
// index without conflict.  Operators then fill in the real SubaccountIDs via
// the CpiTenant update API.
//
// Idempotent: if subaccount_id already exists (i.e. migration already ran),
// this function is a no-op.
func PreAutoMigrate013(db *gorm.DB) error {
	if db.Migrator().HasColumn(&CpiTenant{}, "subaccount_id") {
		// Column already exists — either migration already ran, or this is a
		// fresh database.  Either way, nothing to do.
		return nil
	}

	// Add the column manually without the unique index so we can safely
	// populate it before AutoMigrate creates the constraint.
	if err := db.Exec(
		`ALTER TABLE cpi_tenants ADD COLUMN IF NOT EXISTS subaccount_id TEXT NOT NULL DEFAULT ''`,
	).Error; err != nil {
		return err
	}

	// Assign a unique placeholder to every existing tenant so the unique index
	// can be created successfully.  The "PENDING_" prefix signals to operators
	// that a real SubaccountID must be filled in before bootstrapping.
	if err := db.Exec(
		`UPDATE cpi_tenants SET subaccount_id = 'PENDING_' || id::text WHERE subaccount_id = ''`,
	).Error; err != nil {
		return err
	}

	return nil
}

// PostAutoMigrate013 must be called AFTER AutoMigrate when deploying RFC 013 changes.
//
// AutoMigrate adds the new status columns with an empty-string default (Go zero
// value), but the application expects "missing" as the sentinel for "not yet
// inspected".  This function backfills all existing tenant rows that still have
// the empty-string zero value.
//
// Idempotent: rows already set to "missing" (or any other non-empty value) are
// not touched.
func PostAutoMigrate013(db *gorm.DB) error {
	return db.Exec(`
		UPDATE cpi_tenants SET
			pir_api_status                  = 'missing',
			cas_application_status          = 'missing',
			cas_standard_status             = 'missing',
			cloud_integration_dest_status   = 'missing',
			content_assembly_dest_status    = 'missing',
			transport_management_dest_status = 'missing',
			tms_node_registration_status    = 'missing'
		WHERE
			pir_api_status = ''
	`).Error
}
