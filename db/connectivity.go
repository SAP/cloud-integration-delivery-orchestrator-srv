package db

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ConnectivityCheckResult stores per-dependency connectivity status.
// Composite primary key: (Type, Name).
type ConnectivityCheckResult struct {
	Type      string    `gorm:"primaryKey" json:"type"`
	Name      string    `gorm:"primaryKey" json:"name"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// UpsertConnectivityResult saves or updates a single connectivity check result.
func UpsertConnectivityResult(db *gorm.DB, result ConnectivityCheckResult) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "type"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "message", "checked_at"}),
	}).Create(&result).Error
}

// UpsertConnectivityResults saves or updates multiple results in batch.
func UpsertConnectivityResults(db *gorm.DB, results []ConnectivityCheckResult) error {
	if len(results) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "type"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "message", "checked_at"}),
	}).Create(&results).Error
}

// GetAllConnectivityResults returns all cached check results.
func GetAllConnectivityResults(db *gorm.DB) ([]ConnectivityCheckResult, error) {
	var results []ConnectivityCheckResult
	if err := db.Find(&results).Error; err != nil {
		return nil, err
	}
	return results, nil
}
