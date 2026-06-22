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
