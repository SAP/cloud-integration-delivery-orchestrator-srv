package log

import (
	"os"

	log "github.com/sirupsen/logrus"
)

var logLevel log.Level

func init() {
	log_level, ok := os.LookupEnv("LOG_LEVEL")
	if !ok {
		log.Fatal("error when looking up env LOG_LEVEL")
	}
	switch log_level {
	case "debug":
		logLevel = log.DebugLevel
	case "info":
		logLevel = log.InfoLevel
	case "trace":
		logLevel = log.TraceLevel
	case "warn":
		logLevel = log.WarnLevel
	case "error":
		logLevel = log.ErrorLevel
	}
}
func NewLogger() *log.Logger {

	logger := log.New()
	logger.SetFormatter(&log.JSONFormatter{})

	logger.SetLevel(logLevel)

	return logger
}
