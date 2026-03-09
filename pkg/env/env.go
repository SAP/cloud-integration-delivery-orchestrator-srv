package env

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/cloudfoundry-community/go-cfenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var destinationMap map[string]Destination
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

// Init initializes the env package: loads CF environment, creates logger, fetches destinations.
// Must be called explicitly in main() before using TmsCredential/UaaCredential/Destinations.
func Init() error {
	var err error
	appEnv, err = cfenv.Current()
	if err != nil {
		return fmt.Errorf("failed to load app env: %w", err)
	}
	logLevel = zap.InfoLevel
	logger = NewLogger()
	if err := initDestinations(); err != nil {
		return fmt.Errorf("failed to init destinations: %w", err)
	}
	return nil
}

func Destinations() map[string]Destination {
	return destinationMap
}

// GetDestination retrieves a destination by name
func GetDestination(name string) (*Destination, error) {
	dest, ok := destinationMap[name]
	if !ok {
		return nil, fmt.Errorf("destination '%s' not found", name)
	}
	return &dest, nil
}

// GetJiraDestination retrieves the JIRA service destination named "JIRA_Service"
func GetJiraDestination() (*Destination, error) {
	return GetDestination("JIRA_Service")
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

func UaaCredential() Credentials {
	services, err := appEnv.Services.WithLabel("xsuaa")
	if err != nil || len(services) == 0 {
		Logger().Panic("Failed to get service with label 'xsuaa'")
	}
	service := services[0]
	apiUrl, _ := service.CredentialString("apiurl")
	uaa := service.Credentials

	return Credentials{
		Clientid:     uaa["clientid"].(string),
		Clientsecret: uaa["clientsecret"].(string),
		AuthUrl:      uaa["url"].(string),
		ApiUrl:       apiUrl,
	}
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

type Destination struct {
	Name                string `json:"Name"`
	Type                string `json:"Type"`
	URL                 string `json:"URL"`
	Authentication      string `json:"Authentication"`
	ProxyType           string `json:"ProxyType"`
	TokenServiceURLType string `json:"tokenServiceURLType"`
	TrustAll            string `json:"TrustAll"`
	ClientId            string `json:"clientId"`
	ClientSecret        string `json:"clientSecret"`
	TokenServiceURL     string `json:"tokenServiceURL"`
	User                string `json:"user"`
	Password            string `json:"password"`
	Port                string `json:"port"` // SMTP port, etc.
}

// initDestinations fetches destinations from the destination service.
func initDestinations() error {
	ctx := context.Background()
	services, err := appEnv.Services.WithLabel("destination")
	if err != nil || len(services) == 0 {
		return fmt.Errorf("failed to get service with label 'destination'")
	}
	service := services[0]
	authUrl, _ := service.CredentialString("url")
	if !strings.HasSuffix(authUrl, "/oauth/token") {
		authUrl = fmt.Sprintf("%s/oauth/token", authUrl)
	}
	apiUrl, _ := service.CredentialString("uri")
	clientId, _ := service.CredentialString("clientid")
	clientSecret, _ := service.CredentialString("clientsecret")
	client, err := NewClient(ctx, clientId, clientSecret, authUrl, apiUrl)
	if err != nil {
		return fmt.Errorf("error creating destination client: %w", err)
	}

	apiUrl = fmt.Sprintf("%s/destination-configuration/v1/subaccountDestinations", apiUrl)
	req := &HttpRequest{
		ApiURL: apiUrl,
		Method: http.MethodGet,
	}
	resp, _, err := client.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("error getting subaccount destinations: %w", err)
	}
	var destinations []Destination
	if err := json.Unmarshal(*resp, &destinations); err != nil {
		return fmt.Errorf("failed to unmarshal destinations: %w", err)
	}

	m := make(map[string]Destination)
	for _, v := range destinations {
		m[v.Name] = v
	}
	destinationMap = m
	return nil
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
