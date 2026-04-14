package db

import (
	"database/sql"
	"fmt"
	"os"

	"mmt-delivery/pkg/env"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"moul.io/zapgorm2"
)

var db *gorm.DB

// Conn returns the global database connection.
// Must be called after Connect().
func Conn() *gorm.DB {
	return db
}

// Connect initializes the database connection and runs AutoMigrate.
// Must be called explicitly in main().
func Connect() (*gorm.DB, error) {
	logger := zapgorm2.New(env.Logger().Desugar())

	var conn *sql.DB
	var err error

	dbUri := os.Getenv("LOCAL_POSTGRES_URI")
	if dbUri != "" {
		env.Logger().Info("Connecting to local database (LOCAL_POSTGRES_URI)...")
	} else {
		env.Logger().Info("Connecting to CF-managed database (VCAP_SERVICES)...")
		dbUri = env.PostgreUri()
	}

	conn, err = sql.Open("pgx", dbUri)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	db, err = gorm.Open(
		postgres.New(postgres.Config{Conn: conn}),
		&gorm.Config{Logger: logger},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}
	// AutoMigrate core legacy models plus new lifecycle models
	if err := PreAutoMigrate013(db); err != nil {
		return nil, fmt.Errorf("pre-migration rfc013 failed: %w", err)
	}
	if err := db.AutoMigrate(
		&CpiTenant{}, &DeliveryRule{}, &DeliveryRequest{}, &ArtifactTenantOperation{}, &BatchJob{},
		&Artifact{}, &Condition{}, &VersionCompareSnapshot{}, &VersionCompareIncludedPackage{},
		&IntegrationConfig{},
		// RFC 013: subaccount-centric CpiTenant + bootstrap state machine
		&CentralTmsContext{}, &TenantBootstrapJob{},
	); err != nil {
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}
	if err := PostAutoMigrate013(db); err != nil {
		return nil, fmt.Errorf("post-migration rfc013 failed: %w", err)
	}

	// Seed predefined integration types (idempotent — skips existing records)
	if err := SeedIntegrationConfigs(db); err != nil {
		return nil, fmt.Errorf("failed to seed integration configs: %w", err)
	}

	return db, nil
}
