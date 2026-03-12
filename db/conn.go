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
	remote, ok := os.LookupEnv("REMOTE")
	if ok && remote == "true" {
		env.Logger().Info("Connecting to remote database...")
		dbUri := env.PostgreUri()
		conn, err = sql.Open("pgx", dbUri)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to remote database: %w", err)
		}
	} else {
		env.Logger().Info("Connecting to local database...")
		localDbUri := os.Getenv("LOCAL_POSTGRES_URI")
		if localDbUri == "" {
			return nil, fmt.Errorf("LOCAL_POSTGRES_URI environment variable is not set")
		}
		conn, err = sql.Open("pgx", localDbUri)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to local database: %w", err)
		}
	}

	db, err = gorm.Open(
		postgres.New(postgres.Config{Conn: conn}),
		&gorm.Config{Logger: logger},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}
	// AutoMigrate core legacy models plus new lifecycle models
	if err := db.AutoMigrate(
		&CpiTenant{}, &DeliveryRule{}, &DeliveryRequest{}, &ArtifactTenantOperation{}, &BatchJob{},
		&Artifact{}, &Condition{}, &VersionCompareSnapshot{}, &VersionCompareIncludedPackage{},
	); err != nil {
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}
	return db, nil
}
