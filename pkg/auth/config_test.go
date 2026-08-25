package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOAuthConfig_URLs(t *testing.T) {
	cfg := &OAuthConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		AuthURL:      "https://mysubdomain.authentication.sap.hana.ondemand.com",
	}

	assert.Equal(t, "https://mysubdomain.authentication.sap.hana.ondemand.com/oauth/authorize", cfg.AuthorizeURL())
	assert.Equal(t, "https://mysubdomain.authentication.sap.hana.ondemand.com/oauth/token", cfg.TokenURL())
	assert.Equal(t, "https://mysubdomain.authentication.sap.hana.ondemand.com/logout.do", cfg.LogoutURL())
	assert.Equal(t, "https://mysubdomain.authentication.sap.hana.ondemand.com/userinfo", cfg.UserInfoURL())
}
