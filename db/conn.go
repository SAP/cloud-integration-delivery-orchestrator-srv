package db

import (
	"database/sql"

	"github.wdf.sap.corp/maco-mmt/maco-deploy/env"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"moul.io/zapgorm2"
)

var logger = zapgorm2.New(env.Logger().Desugar())

func init() {
	var conn *sql.DB
	var err error
	local := true
	if !local {
		dbUri := env.PostgreUri()
		conn, _ = sql.Open("postgres", dbUri)
	} else {
		conn, err = sql.Open("postgres", "postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable")
		if err != nil {
			panic("failed to connect database" + err.Error())
		}
	}

	db, err = gorm.Open(
		postgres.New(postgres.Config{Conn: conn}),
		&gorm.Config{Logger: logger},
	)
	if err != nil {
		panic("failed to connect database" + err.Error())
	}
	db.AutoMigrate(&Job{}, &ImportStep{}, &DeployStep{}, &ExecutionLog{})
}

var db *gorm.DB

func Conn() *gorm.DB {
	return db
}
