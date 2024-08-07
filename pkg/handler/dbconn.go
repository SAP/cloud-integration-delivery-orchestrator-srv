package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	pgxmig "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.wdf.sap.corp/maco-mmt/maco-deploy/db"
)

var dbSource string

const dbDriver = "postgres"

func init() {
	db_host, ok := os.LookupEnv("DB_HOST")
	if !ok {
		logger.Fatal("error when looking up env DB_HOST")
	}
	db_port, ok := os.LookupEnv("DB_PORT")
	if !ok {
		logger.Fatal("error when looking up env DB_PORT")
	}
	db_name, ok := os.LookupEnv("DB_NAME")
	if !ok {
		logger.Fatal("error when looking up env DB_NAME")
	}

	db_user, ok := os.LookupEnv("DB_USER")
	if !ok {
		logger.Fatal("error when looking up env DB_USER")
	}
	db_password, ok := os.LookupEnv("DB_PASSWORD")
	if !ok {
		logger.Fatal("error when looking up env DB_PASSWORD")
	}
	dbSource = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", db_user, db_password, db_host, db_port, db_name)
	context := context.Background()
	_, errDBconn := pgx.Connect(context, dbSource)
	if errDBconn != nil {
		logger.Fatalf("Failed to connect to database, error message is %s", errDBconn)
	}

}

type DBClient struct {
	ctx    context.Context
	DBConn *pgx.Conn
	Query  *db.Queries
}

func NewDBClient(ctx *gin.Context) DBClient {
	dbConn, errDBconn := pgx.Connect(ctx, dbSource)
	if errDBconn != nil {
		logger.Errorf("error when connecting to the database, please contact your system administrator, error message is %s", errDBconn)
		ctx.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"msg":    "error when connecting to the database, please contact your system administrator",
			"code":   http.StatusServiceUnavailable,
		})
	}

	return DBClient{
		ctx:    ctx,
		DBConn: dbConn,
		Query:  db.New(dbConn),
	}
}

func (d *DBClient) dbmigrate() {

	pxConfig := &pgxmig.Config{
		DatabaseName:    "macodeploy",
		MigrationsTable: "user",
	}
	sqlInstance, error := sql.Open(dbDriver, dbSource)
	if error != nil {
		logger.Fatalf("error when create db instance, error message is %s", error)
	}

	pxdriver, error2 := pgxmig.WithInstance(sqlInstance, pxConfig)

	if error2 != nil {
		logger.Fatalf("error when px instance driver, error message is %s", error2)

	}
	migrator, error3 := migrate.NewWithDatabaseInstance("file://../db/migrations", "macodeploy", pxdriver)

	if error3 != nil {
		logger.Fatalf("error when creating migratin instance, error message %s", error3)

	}
	migrator.Up()
}
