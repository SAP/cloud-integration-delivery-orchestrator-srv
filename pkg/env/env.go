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

func init() {
	var err error
	appEnv, err = cfenv.Current()
	if err != nil {
		panic("Failed to load app env: " + err.Error())
	}
	logLevel = zap.InfoLevel
	logger = NewLogger()
	initDestinations()
}

func Logger() *zap.SugaredLogger {
	return logger
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

// Get Destinations(including credentials)
func initDestinations() {
	ctx := context.Background()
	services, err := appEnv.Services.WithLabel("destination")
	if err != nil || len(services) == 0 {
		logger.Panic("Failed to get service with label 'destination'")
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
		logger.Panic("Error Creating destination client")
		return
	}

	apiUrl = fmt.Sprintf("%s/destination-configuration/v1/subaccountDestinations", apiUrl)
	req := &HttpRequest{
		Ctx:    ctx,
		ApiURL: apiUrl,
		Method: http.MethodGet,
	}
	resp, _, err := client.Do(req)
	if err != nil {
		logger.Panic("Error while Get subaccount destinations")
	}
	var destinations []Destination
	if err := json.Unmarshal(*resp, &destinations); err != nil {
		logger.Panic("Failed to unmarchal destination")
	}

	m := make(map[string]Destination)
	for _, v := range destinations {
		m[v.Name] = v
	}
	destinationMap = m
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
