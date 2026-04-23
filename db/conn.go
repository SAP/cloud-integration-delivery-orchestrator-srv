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

	// Step 1: AutoMigrate adds new columns to artifact_tenant_operations.
	// The former artifacts table and artifact_id FK are removed after backfill (below).
	if err := db.AutoMigrate(
		&CpiTenant{}, &DeliveryRule{}, &DeliveryRequest{}, &ArtifactTenantOperation{}, &BatchJob{},
		&Condition{}, &VersionCompareSnapshot{}, &VersionCompareIncludedPackage{},
		&IntegrationConfig{},
		// RFC 013: subaccount-centric CpiTenant + bootstrap state machine
		&CentralTmsContext{}, &TenantBootstrapJob{},
	); err != nil {
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	// Step 2: RFC-015 — backfill flattened artifact fields from the artifacts table.
	// Runs only while artifact_id column still exists (idempotent: WHERE artifact_name = '').
	// Once all rows are backfilled, drop the FK column and the artifacts table.
	if db.Migrator().HasColumn(&ArtifactTenantOperation{}, "artifact_id") {
		// Build SET clause defensively: package_name and package_version were added
		// to the artifacts table later and may not exist in older deployments.
		hasPackageName := db.Migrator().HasColumn("artifacts", "package_name")
		hasPackageVersion := db.Migrator().HasColumn("artifacts", "package_version")

		setClause := "artifact_name = a.name, artifact_type = a.type, package_id = a.package_id"
		if hasPackageName {
			setClause += ", package_name = a.package_name"
		}
		if hasPackageVersion {
			setClause += ", package_version = a.package_version"
		}

		sql := `UPDATE artifact_tenant_operations op SET ` + setClause + `
			FROM artifacts a
			WHERE op.artifact_id = a.id
			  AND (op.artifact_name IS NULL OR op.artifact_name = '')`
		if err := db.Exec(sql).Error; err != nil {
			return nil, fmt.Errorf("RFC-015 backfill failed: %w", err)
		}

		// Verify all rows were backfilled before dropping the column.
		// Rows with artifact_name still empty have no matching artifacts row (orphaned ops);
		// fail fast so they are not silently dropped.
		var unbackfilled int64
		if err := db.Model(&ArtifactTenantOperation{}).Where("artifact_name = '' OR artifact_name IS NULL").Count(&unbackfilled).Error; err != nil {
			return nil, fmt.Errorf("RFC-015 backfill verification failed: %w", err)
		}
		if unbackfilled > 0 {
			return nil, fmt.Errorf("RFC-015 backfill incomplete: %d artifact_tenant_operations row(s) still have empty artifact_name — check for orphaned ops (artifact_id with no matching artifacts row)", unbackfilled)
		}

		if err := db.Migrator().DropColumn(&ArtifactTenantOperation{}, "artifact_id"); err != nil {
			return nil, fmt.Errorf("RFC-015 drop artifact_id column failed: %w", err)
		}

		if db.Migrator().HasTable("artifacts") {
			if err := db.Exec("DROP TABLE artifacts").Error; err != nil {
				return nil, fmt.Errorf("RFC-015 drop artifacts table failed: %w", err)
			}
		}
	}

	// Seed predefined integration types (idempotent — skips existing records)
	if err := SeedIntegrationConfigs(db); err != nil {
		return nil, fmt.Errorf("failed to seed integration configs: %w", err)
	}

	return db, nil
}
