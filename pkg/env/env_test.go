package env

import (
	"testing"

	cfenv "github.com/cloudfoundry-community/go-cfenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXsuaaByPlan(t *testing.T) {
	// Set up mock appEnv with two xsuaa services
	appEnv = &cfenv.App{
		Services: cfenv.Services{
			"xsuaa": []cfenv.Service{
				{
					Name:  "cpi-delivery-uaa",
					Label: "xsuaa",
					Plan:  "application",
					Credentials: map[string]interface{}{
						"clientid":     "oauth-client",
						"clientsecret": "oauth-secret",
						"url":          "https://auth.example.com",
						"apiurl":       "https://api.auth.example.com",
					},
				},
				{
					Name:  "uaa-api",
					Label: "xsuaa",
					Plan:  "apiaccess",
					Credentials: map[string]interface{}{
						"clientid":     "api-client",
						"clientsecret": "api-secret",
						"url":          "https://auth.example.com",
						"apiurl":       "https://api.auth.example.com",
					},
				},
			},
		},
	}

	oauth := OAuthUaaCredential()
	assert.Equal(t, "oauth-client", oauth.Clientid)
	assert.Equal(t, "oauth-secret", oauth.Clientsecret)

	api := ApiUaaCredential()
	assert.Equal(t, "api-client", api.Clientid)
}

func TestXsuaaByPlan_PanicOnMissingPlan(t *testing.T) {
	appEnv = &cfenv.App{
		Services: cfenv.Services{
			"xsuaa": []cfenv.Service{
				{
					Label: "xsuaa",
					Plan:  "apiaccess",
					Credentials: map[string]interface{}{
						"clientid": "x", "clientsecret": "x", "url": "x", "apiurl": "x",
					},
				},
			},
		},
	}
	require.Panics(t, func() { OAuthUaaCredential() })
}

func TestAnsCredential(t *testing.T) {
	t.Run("parses credentials and strips oauth_url path", func(t *testing.T) {
		appEnv = &cfenv.App{
			Services: cfenv.Services{
				"alert-notification": []cfenv.Service{
					{
						Label: "alert-notification",
						Plan:  "free",
						Credentials: map[string]interface{}{
							"client_id":     "sb-test-id",
							"client_secret": "test-secret",
							"oauth_url":     "https://mysubaccount.authentication.sap.hana.ondemand.com/oauth/token?grant_type=client_credentials",
							"url":           "https://clm-sl-ans-canary-ans-service-api.cfapps.sap.hana.ondemand.com",
						},
					},
				},
			},
		}
		creds := AnsCredential()
		require.NotNil(t, creds)
		assert.Equal(t, "sb-test-id", creds.Clientid)
		assert.Equal(t, "test-secret", creds.Clientsecret)
		assert.Equal(t, "https://mysubaccount.authentication.sap.hana.ondemand.com", creds.AuthUrl, "oauth_url should be stripped to base URL")
		assert.Equal(t, "https://clm-sl-ans-canary-ans-service-api.cfapps.sap.hana.ondemand.com", creds.ApiUrl)
	})

	t.Run("handles oauth_url without path", func(t *testing.T) {
		appEnv = &cfenv.App{
			Services: cfenv.Services{
				"alert-notification": []cfenv.Service{
					{
						Label: "alert-notification",
						Credentials: map[string]interface{}{
							"client_id":     "id",
							"client_secret": "secret",
							"oauth_url":     "https://auth.example.com",
							"url":           "https://ans.example.com",
						},
					},
				},
			},
		}
		creds := AnsCredential()
		require.NotNil(t, creds)
		assert.Equal(t, "https://auth.example.com", creds.AuthUrl, "oauth_url without path should be unchanged")
	})

	t.Run("returns nil when not bound", func(t *testing.T) {
		appEnv = &cfenv.App{Services: cfenv.Services{}}
		assert.Nil(t, AnsCredential())
	})

	t.Run("returns nil when credentials incomplete", func(t *testing.T) {
		appEnv = &cfenv.App{
			Services: cfenv.Services{
				"alert-notification": []cfenv.Service{
					{
						Label:       "alert-notification",
						Credentials: map[string]interface{}{"client_id": "id"},
					},
				},
			},
		}
		assert.Nil(t, AnsCredential())
	})
}
