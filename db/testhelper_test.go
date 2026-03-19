package db

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	var dialector gorm.Dialector

	if uri := os.Getenv("LOCAL_POSTGRES_URI"); uri != "" {
		conn, err := sql.Open("pgx", uri)
		if err != nil {
			fmt.Printf("FATAL: failed to open postgres: %v\n", err)
			os.Exit(1)
		}
		dialector = postgres.New(postgres.Config{Conn: conn})
		fmt.Fprintln(os.Stderr, "INFO: using PostgreSQL for tests")
	} else {
		dialector = sqlite.Open("file::memory:?cache=shared")
		fmt.Fprintln(os.Stderr, "INFO: using SQLite (in-memory) for tests")
	}

	var err error
	testDB, err = gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Printf("FATAL: failed to initialize gorm: %v\n", err)
		os.Exit(1)
	}

	if err := testDB.AutoMigrate(&VersionCompareSnapshot{}, &VersionCompareIncludedPackage{}); err != nil {
		fmt.Printf("FATAL: failed to migrate: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// cleanSnapshotsByRuleIDs registers a t.Cleanup that deletes only snapshots with
// the given DeliveryRuleIDs, avoiding interference with other data in the shared DB.
func cleanSnapshotsByRuleIDs(t *testing.T, ruleIDs ...uint) {
	t.Helper()
	t.Cleanup(func() {
		testDB.Unscoped().Where("delivery_rule_id IN ?", ruleIDs).Delete(&VersionCompareSnapshot{})
	})
}
