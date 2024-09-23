package db

import (
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Job struct {
	gorm.Model
	Name        string
	Description string
	Status      string
	Type        string
}

type ImportStep struct {
	gorm.Model        `mapstructure:",squash"`
	JobId             uint
	Sequence          uint
	Status            string
	TransportNodeId   uint
	TransportNodeName string
	TransportRequests pq.Int32Array `gorm:"type:integer[]"`
	ActionId          uint
}

type DeployStep struct {
	gorm.Model       `mapstructure:",squash"`
	JobId            uint
	Sequence         uint
	Status           string
	Endpoint         string
	PackageId        string
	ArtifactIds      pq.StringArray `gorm:"type:varchar[]"`
	ArtifactTypes    pq.StringArray `gorm:"type:varchar[]"`
	ArtifactVersions pq.StringArray `gorm:"type:varchar[]"`
	TaskIds          pq.StringArray `gorm:"type:varchar[]"`
}

type ArtifactStatus struct {
	ID           uint `gorm:"primaryKey"`
	JobId        uint
	StepId       uint
	ArtifactType string
	ArifactId    string
	TaskId       string
	Status       string
}
type VcappServices struct {
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
