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
	logger.IgnoreRecordNotFoundError = true // ErrRecordNotFound is normal (e.g. 404), log as Warn not Error

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

	// Step 1: AutoMigrate — creates/updates tables to match current struct definitions.
	if err := db.AutoMigrate(
		&CpiTenant{}, &DeliveryRule{}, &DeliveryRequest{}, &ArtifactTenantOperation{}, &BatchJob{},
		&Condition{}, &VersionCompareSnapshot{}, &VersionCompareIncludedPackage{},
		&JiraConfig{},
		&CentralTmsContext{}, &TenantBootstrapJob{},
		&GitRepoConfig{}, &GitArtifactSnapshot{},
	); err != nil {
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return db, nil
}
