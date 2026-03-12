package db

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB is the shared *gorm.DB instance for all tests in the db package.
var testDB *gorm.DB

// TestMain sets up the test database connection and runs all tests.
// Requires LOCAL_POSTGRES_URI env var pointing to a running PostgreSQL instance.
func TestMain(m *testing.M) {
	uri := os.Getenv("LOCAL_POSTGRES_URI")
	if uri == "" {
		fmt.Println("SKIP: LOCAL_POSTGRES_URI not set, skipping db tests")
		os.Exit(0)
	}

	conn, err := sql.Open("pgx", uri)
	if err != nil {
		fmt.Printf("FATAL: failed to open database: %v\n", err)
		os.Exit(1)
	}

	testDB, err = gorm.Open(
		postgres.New(postgres.Config{Conn: conn}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		fmt.Printf("FATAL: failed to initialize gorm: %v\n", err)
		os.Exit(1)
	}

	// Migrate only the model under test
	if err := testDB.AutoMigrate(&VersionCompareSnapshot{}); err != nil {
		fmt.Printf("FATAL: failed to migrate: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	// Cleanup: drop the test table after all tests
	testDB.Exec("DROP TABLE IF EXISTS version_compare_snapshots")

	os.Exit(code)
}

// cleanSnapshots deletes all rows from version_compare_snapshots.
// Call at the start of each test for isolation.
func cleanSnapshots(t *testing.T) {
	t.Helper()
	if err := testDB.Exec("DELETE FROM version_compare_snapshots").Error; err != nil {
		t.Fatalf("failed to clean snapshots: %v", err)
	}
}
