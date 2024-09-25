package db

import (
	"fmt"
	"os"

	"github.wdf.sap.corp/maco-mmt/maco-deploy/env"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"moul.io/zapgorm2"
)

var logger = zapgorm2.New(env.Logger().Desugar())

func init() {
	//dsn := "host=127.0.0.1 user=postgres password=passw0rd dbname=macodeploy port=5432 sslmode=disable TimeZone=Asia/Shanghai"
	var dsn string
	var err error
	local := true
	if !local {

		pgCredentials := env.PostgreCred()
		pgHost := pgCredentials.Hostname
		pgDBname := pgCredentials.Dbname
		pgPort := pgCredentials.Port
		pgUser := pgCredentials.Username
		pgPass := pgCredentials.Password
		pgSslRootCert := pgCredentials.Sslrootcert
		if pgSslRootCert != "" {
			err := os.WriteFile("root.crt", []byte(pgSslRootCert), 0644)
			if err != nil {
				logger.ZapLogger.Sugar().Fatalf("failed to write root.crt, %s", err)
			}
		}
		pgSslCert := pgCredentials.Sslcert

		if pgSslCert != "" {
			err := os.WriteFile("client.crt", []byte(pgSslCert), 0644)
			if err != nil {
				logger.ZapLogger.Sugar().Fatalf("failed to write client.crt, %s", err)
			}
		}

		// pgSslKey := vcapServices.Destination[0].Credentials.Verificationkey

		// if pgSslKey != "" {
		// 	err := os.WriteFile("client.key", []byte(pgSslKey), 0644)
		// 	if err != nil {
		// 		logger.ZapLogger.Sugar().Fatalf("failed to write client.key, %s", err)
		// 	}
		// }
		dsn = fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=verify-full sslrootcert=root.crt sslkey=client.key sslcert=client.crt", pgHost, pgUser, pgPass, pgDBname, pgPort)
	} else {
		dsn = "host=127.0.0.1 user=postgres password=passw0rd dbname=macodeploy port=5432 sslmode=disable"
	}

	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger,
	})
	if err != nil {
		panic(err)
	}
	db.AutoMigrate(&Job{}, &ImportStep{}, &DeployStep{}, &ExecutionLog{})

}

var db *gorm.DB

func Conn() *gorm.DB {
	return db
}
