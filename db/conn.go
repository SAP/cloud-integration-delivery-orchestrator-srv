package db

import (
	"database/sql"
	"os"

	"mmt-delivery/pkg/env"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"moul.io/zapgorm2"
)

var logger = zapgorm2.New(env.Logger().Desugar())

func init() {
	var conn *sql.DB
	var err error
	remote, ok := os.LookupEnv("REMOTE")
	if ok && remote == "true" {
		env.Logger().Info("Connecting to remote database...")
		dbUri := env.PostgreUri()
		conn, err = sql.Open("pgx", dbUri)
		if err != nil {
			panic("failed to connect to remote database" + err.Error())
		}
	} else {
		conn, err = sql.Open("pgx", "postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable")
		if err != nil {
			panic("failed to connect to local database" + err.Error())
		}
	}

	db, err = gorm.Open(
		postgres.New(postgres.Config{Conn: conn}),
		&gorm.Config{Logger: logger},
	)
	if err != nil {
		panic("failed to connect database" + err.Error())
	}
	// AutoMigrate core legacy models plus new lifecycle models
	if err := db.AutoMigrate(
		&CpiTenant{}, &DeliveryRule{}, &DeliveryRequest{}, &ArtifactTenantOperation{}, &BatchJob{},
		&Artifact{},
	); err != nil {
		panic("failed to migrate: " + err.Error())
	}
}

var db *gorm.DB

func Conn() *gorm.DB {
	return db
}
