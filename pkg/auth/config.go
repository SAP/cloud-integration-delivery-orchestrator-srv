package auth

import (
	"fmt"
	"strings"

	"mmt-delivery/pkg/env"
)

// OAuthConfig holds XSUAA application-plan credentials for Authorization Code Flow.
// redirect_uri is NOT stored here — it is derived dynamically from each request
// (same behavior as SAP Approuter).
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	AuthURL      string // base URL, e.g. https://subdomain.authentication.eu10.hana.ondemand.com
}

func (c *OAuthConfig) AuthorizeURL() string { return c.AuthURL + "/oauth/authorize" }
func (c *OAuthConfig) TokenURL() string     { return c.AuthURL + "/oauth/token" }
func (c *OAuthConfig) LogoutURL() string    { return c.AuthURL + "/logout.do" }
func (c *OAuthConfig) UserInfoURL() string  { return c.AuthURL + "/userinfo" }

// LoadOAuthConfigFromEnv loads XSUAA application-plan credentials from VCAP_SERVICES.
func LoadOAuthConfigFromEnv() (*OAuthConfig, error) {
	cred := env.OAuthUaaCredential()
	if cred.Clientid == "" || cred.Clientsecret == "" || cred.AuthUrl == "" {
		return nil, fmt.Errorf("auth: incomplete XSUAA application-plan credentials")
	}
	return &OAuthConfig{
		ClientID:     cred.Clientid,
		ClientSecret: cred.Clientsecret,
		AuthURL:      strings.TrimRight(cred.AuthUrl, "/"),
	}, nil
}
