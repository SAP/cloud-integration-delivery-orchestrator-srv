package handler

import (
	"context"
	"database/sql"

	log "github.com/sirupsen/logrus"

	"github.com/golang-migrate/migrate/v4"
	pgxmig "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

const (
	dbDriver = "postgres"
	dbSource = "postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable"
)

type DBClient struct {
	ctx    context.Context
	DBConn *pgx.Conn
	Query  *db.Queries
}

func NewDBClient(ctx context.Context) (DBClient, error) {
	dbConn, errDBconn := pgx.Connect(ctx, dbSource)
	if errDBconn != nil {
		log.Fatal("error connecting to db,", errDBconn)
	}

	return DBClient{
		ctx:    ctx,
		DBConn: dbConn,
		Query:  db.New(dbConn),
	}, nil
}

func (d *DBClient) dbmigrate() {

	pxConfig := &pgxmig.Config{
		DatabaseName:    "macodeploy",
		MigrationsTable: "user",
	}
	sqlInstance, error := sql.Open(dbDriver, dbSource)
	if error != nil {
		log.Fatalf("error when create db instance, error message is %s", error)
	}

	pxdriver, error2 := pgxmig.WithInstance(sqlInstance, pxConfig)

	if error2 != nil {
		log.Fatalf("error when px instance driver, error message is %s", error2)

	}
	migrator, error3 := migrate.NewWithDatabaseInstance("file://../db/migrations", "macodeploy", pxdriver)

	if error3 != nil {
		log.Fatalf("error when creating migratin instance, error message %s", error3)

	}
	migrator.Up()
}
