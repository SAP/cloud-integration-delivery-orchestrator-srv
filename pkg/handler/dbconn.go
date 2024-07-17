package handler

import (
	"context"
	"fmt"
	"log"

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

func NewDBClient(ctx context.Context, driver string, dbHost string, dbPort string, dbUser string, dbPassword string, dbname string) (DBClient, error) {
	dbSource := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, dbHost, dbPort, dbname)
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
