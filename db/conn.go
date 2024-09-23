package db

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func init() {
	dsn := "host=127.0.0.1 user=postgres password=passw0rd dbname=macodeploy port=5432 sslmode=disable TimeZone=Asia/Shanghai"
	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
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
