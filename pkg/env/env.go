package env

import (
	"context"
	"fmt"
	"os"

	"github.com/cloudfoundry-community/go-cfenv"
	cpiotel "mmt-delivery/pkg/otel"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var logLevel zapcore.Level
var logger *zap.SugaredLogger

var appEnv *cfenv.App

// Logger returns the package-level logger.
// Safe to call before Init() — returns a fallback logger if not yet initialized.
func Logger() *zap.SugaredLogger {
	if logger == nil {
		logger = NewLogger()
	}
	return logger
}

// L returns a logger enriched with trace_id and span_id from ctx.
// When ctx has no active span, returns the plain logger unchanged.
func L(ctx context.Context) *zap.SugaredLogger {
	return cpiotel.WithTrace(ctx, Logger())
}

// Init initializes the env package: loads CF environment and creates logger.
// Must be called explicitly in main() before using TmsCredential/UaaCredential/NewDestinationResolver.
func Init() error {
	var err error
	appEnv, err = cfenv.Current()
	if err != nil {
		return fmt.Errorf("failed to load app env: %w", err)
	}
	logLevel = zap.InfoLevel
	logger = NewLogger()
	return nil
}

// AppEnv returns the parsed CF application environment.
// Returns nil if Init() has not been called yet.
func AppEnv() *cfenv.App {
	return appEnv
}

func TmsCredential() Credentials {
	services, err := appEnv.Services.WithLabel("transport")
	if err != nil || len(services) == 0 {
		Logger().Panic("Failed to get service with label 'transport'")
	}
	service := services[0]
	apiUrl, _ := service.CredentialString("uri")
	uaa, _ := service.Credentials["uaa"].(map[string]interface{})

	return Credentials{
		Clientid:     uaa["clientid"].(string),
		Clientsecret: uaa["clientsecret"].(string),
		AuthUrl:      uaa["url"].(string),
		ApiUrl:       apiUrl,
	}
}

// xsuaaByPlan finds the xsuaa service binding with the given plan name and
// returns its credentials. Panics with a descriptive message if not found.
func xsuaaByPlan(plan string) Credentials {
	services, err := appEnv.Services.WithLabel("xsuaa")
	if err != nil || len(services) == 0 {
		Logger().Panic("Failed to get service with label 'xsuaa'")
	}
	for _, svc := range services {
		if svc.Plan == plan {
			apiUrl, _ := svc.CredentialString("apiurl")
			uaa := svc.Credentials
			return Credentials{
				Clientid:     uaa["clientid"].(string),
				Clientsecret: uaa["clientsecret"].(string),
				AuthUrl:      uaa["url"].(string),
				ApiUrl:       apiUrl,
			}
		}
	}
	Logger().Panicf("Failed to find xsuaa service binding with plan '%s'", plan)
	panic("unreachable") // satisfy compiler
}

// OAuthUaaCredential returns credentials from the xsuaa binding with plan="application".
// Used for OAuth2 login flows.
func OAuthUaaCredential() Credentials {
	return xsuaaByPlan("application")
}

// ApiUaaCredential returns credentials from the xsuaa binding with plan="apiaccess".
// Used for SCIM API calls.
func ApiUaaCredential() Credentials {
	return xsuaaByPlan("apiaccess")
}

func PostgreUri() string {
	services, err := appEnv.Services.WithLabel("postgresql-db")
	if err != nil || len(services) == 0 {
		panic("Failed to get service with label 'postgresql-db'")
	}
	uri, _ := services[0].CredentialString("uri")
	return uri

}

type Credentials struct {
	Clientid     string
	Clientsecret string
	AuthUrl      string
	ApiUrl       string
}

type PgCredentials struct {
	Dbname      string `json:"dbname"`
	Hostname    string `json:"hostname"`
	Password    string `json:"password"`
	Port        string `json:"port"`
	Sslcert     string `json:"sslcert"`
	Sslrootcert string `json:"sslrootcert"`
	URI         string `json:"uri"`
	Urls        struct {
		APIServer string `json:"api_server"`
	} `json:"urls"`
	Username string `json:"username"`
}

func NewLogger() *zap.SugaredLogger {
	levelConfig := zap.NewAtomicLevel()
	levelConfig.SetLevel(logLevel)

	config := zap.NewProductionEncoderConfig()

	config.EncodeTime = zapcore.RFC3339TimeEncoder
	fileEncoder := zapcore.NewJSONEncoder(config)
	core := zapcore.NewTee(zapcore.NewCore(fileEncoder, zapcore.AddSync(os.Stdout), logLevel))
	logger := zap.New(core)
	return logger.Sugar()
}
