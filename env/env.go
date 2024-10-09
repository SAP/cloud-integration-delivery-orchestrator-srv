package env

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var destinationMap map[string]Destination
var vcap VcapServices
var logLevel zapcore.Level
var logger *zap.SugaredLogger

func init() {
	logLevel = zap.InfoLevel
	logger = NewLogger()
	vcap = readEnv()
	destinations()

}
func Logger() *zap.SugaredLogger {
	return logger
}

func Destinations() map[string]Destination {
	return destinationMap
}
func TmsCred() Credentials {
	credential := vcap.Transport[0].Credentials
	return Credentials{
		Clientid:     credential.Uaa.Clientid,
		Clientsecret: credential.Uaa.Clientsecret,
		AuthUrl:      credential.Uaa.URL,
		ApiUrl:       credential.URI,
	}
}

func PostgreCred() PgCredentials {
	return vcap.PostgresqlDb[0].Credentials
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
}

func readEnv() VcapServices {
	var content []byte
	logger.Info("Reading VCAP_SERVICES")
	envVal, ok := os.LookupEnv("VCAP_SERVICES")
	if !ok { // read from dafault-env.json
		logger.Info("failed to read env VCAP_SERVICES. Try to read file default-env.json")
		file, err := os.Open("default-env.json")
		if err != nil {
			logger.Panic("can not find default-env.json")
		}
		defer file.Close()
		content, err = io.ReadAll(file)
		if err != nil {
			logger.Panic("Faild to read file default-env.json")
		}
		var env struct {
			VapServices VcapServices `json:"VCAP_SERVICES"`
		}
		if err := json.Unmarshal(content, &env); err != nil {
			panic("")
		}
		return env.VapServices
	}
	var vcap VcapServices
	content = []byte(envVal)
	if err := json.Unmarshal(content, &vcap); err != nil {
		panic("Failed to unmarshal VCAP_SERVICES: " + err.Error())
	}
	return vcap
}

// Get Destinations(including credentials)
func destinations() {
	ctx := context.Background()
	credential := vcap.Destination[0].Credentials
	apiUrl := credential.URI
	authUrl := fmt.Sprintf("%s/oauth/token", credential.URL)
	client, err := NewClient(ctx,
		credential.Clientid,
		credential.Clientsecret,
		authUrl,
		apiUrl,
	)
	if err != nil {
		logger.Panic("Error Creating destination client")
	}
	apiUrl = fmt.Sprintf("%s/destination-configuration/v1/subaccountDestinations", apiUrl)
	req := &HttpRequest{
		Ctx:    ctx,
		ApiURL: apiUrl,
		Method: http.MethodGet,
	}
	resp, err := client.Do(req)
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

type VcapServices struct {
	Destination []struct {
		BindingGUID string      `json:"binding_guid"`
		BindingName interface{} `json:"binding_name"`
		Credentials struct {
			Clientid        string `json:"clientid"`
			Clientsecret    string `json:"clientsecret"`
			CredentialType  string `json:"credential-type"`
			Identityzone    string `json:"identityzone"`
			Instanceid      string `json:"instanceid"`
			Tenantid        string `json:"tenantid"`
			Tenantmode      string `json:"tenantmode"`
			Uaadomain       string `json:"uaadomain"`
			URI             string `json:"uri"`
			URL             string `json:"url"`
			Verificationkey string `json:"verificationkey"`
			Xsappname       string `json:"xsappname"`
		} `json:"credentials"`
		InstanceGUID   string        `json:"instance_guid"`
		InstanceName   string        `json:"instance_name"`
		Label          string        `json:"label"`
		Name           string        `json:"name"`
		Plan           string        `json:"plan"`
		Provider       interface{}   `json:"provider"`
		SyslogDrainURL interface{}   `json:"syslog_drain_url"`
		Tags           []string      `json:"tags"`
		VolumeMounts   []interface{} `json:"volume_mounts"`
	} `json:"destination"`
	PostgresqlDb []struct {
		BindingGUID string      `json:"binding_guid"`
		BindingName interface{} `json:"binding_name"`
		Credentials struct {
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
		} `json:"credentials"`
		InstanceGUID   string        `json:"instance_guid"`
		InstanceName   string        `json:"instance_name"`
		Label          string        `json:"label"`
		Name           string        `json:"name"`
		Plan           string        `json:"plan"`
		Provider       interface{}   `json:"provider"`
		SyslogDrainURL interface{}   `json:"syslog_drain_url"`
		Tags           []string      `json:"tags"`
		VolumeMounts   []interface{} `json:"volume_mounts"`
	} `json:"postgresql-db"`
	Transport []struct {
		BindingGUID string      `json:"binding_guid"`
		BindingName interface{} `json:"binding_name"`
		Credentials struct {
			Uaa struct {
				Apiurl            string `json:"apiurl"`
				Clientid          string `json:"clientid"`
				Clientsecret      string `json:"clientsecret"`
				CredentialType    string `json:"credential-type"`
				Identityzone      string `json:"identityzone"`
				Identityzoneid    string `json:"identityzoneid"`
				Sburl             string `json:"sburl"`
				ServiceInstanceID string `json:"serviceInstanceId"`
				Subaccountid      string `json:"subaccountid"`
				Tenantid          string `json:"tenantid"`
				Tenantmode        string `json:"tenantmode"`
				Uaadomain         string `json:"uaadomain"`
				URL               string `json:"url"`
				Verificationkey   string `json:"verificationkey"`
				Xsappname         string `json:"xsappname"`
				Zoneid            string `json:"zoneid"`
			} `json:"uaa"`
			URI string `json:"uri"`
		} `json:"credentials"`
		InstanceGUID   string        `json:"instance_guid"`
		InstanceName   string        `json:"instance_name"`
		Label          string        `json:"label"`
		Name           string        `json:"name"`
		Plan           string        `json:"plan"`
		Provider       interface{}   `json:"provider"`
		SyslogDrainURL interface{}   `json:"syslog_drain_url"`
		Tags           []string      `json:"tags"`
		VolumeMounts   []interface{} `json:"volume_mounts"`
	} `json:"transport"`
}
