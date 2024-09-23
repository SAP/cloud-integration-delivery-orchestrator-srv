package db

import (
	"encoding/json"
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() {
	//dsn := "host=127.0.0.1 user=postgres password=passw0rd dbname=macodeploy port=5432 sslmode=disable TimeZone=Asia/Shanghai"
	var dsn string
	if vcapServicesEnv, ok := os.LookupEnv("VCAP_SERVICES"); ok {
		var vcapServices VcappServices
		json.Unmarshal([]byte(vcapServicesEnv), &vcapServices)
		postgresCredentials := vcapServices.PostgresqlDb[0].Credentials
		pgHost := postgresCredentials.Hostname
		pgDBname := postgresCredentials.Dbname
		pgPort := postgresCredentials.Port
		pgUser := postgresCredentials.Username
		pgPass := postgresCredentials.Password
		pgSslRootCert := postgresCredentials.Sslrootcert
		if pgSslRootCert != "" {
			err := os.WriteFile("root.crt", []byte(pgSslRootCert), 0644)
			if err != nil {
				panic(err)
			}
		}
		pgSslCert := postgresCredentials.Sslcert

		if pgSslCert != "" {
			err := os.WriteFile("client.crt", []byte(pgSslCert), 0644)
			if err != nil {
				panic(err)
			}
		}

		pgSslKey := vcapServices.Destination[0].Credentials.Verificationkey

		if pgSslKey != "" {
			err := os.WriteFile("client.key", []byte(pgSslKey), 0644)
			if err != nil {
				panic(err)
			}
		}
		dsn = fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=verify-full sslrootcert=root.crt sslkey=client.key sslcert=client.crt", pgHost, pgUser, pgPass, pgDBname, pgPort)
	} else {
		dsn = "host=127.0.0.1 user=postgres password=passw0rd dbname=macodeploy port=5432 sslmode=disable"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		panic(err)
	}
	db.AutoMigrate(&Job{}, &ImportStep{}, &DeployStep{}, &ArtifactStatus{})

}

var db *gorm.DB

func Conn() *gorm.DB {
	return db
}
