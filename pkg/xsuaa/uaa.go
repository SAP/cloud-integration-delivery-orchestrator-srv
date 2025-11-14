package xsuaa

import (
	"context"
	"encoding/json"
	"fmt"
	"mmt-delivery/pkg/env"
	"net/http"
	"net/url"
	"time"
)

type UaaClient struct {
	env.HttpClient
}

var logger = env.Logger()

func NewClient(c context.Context) (*UaaClient, error) {
	v := env.UaaCredential()
	client, err := env.NewClient(c, v.Clientid, v.Clientsecret, v.AuthUrl, v.ApiUrl)
	return &UaaClient{*client}, err
}

// search uaa user by email, 'co' operator(https://simplecloud.info/specs/draft-scim-api-01.html#query-resources)
func (uaa *UaaClient) SearchUserByEmail(email string) ([]UserInfo, error) {
	childCtx, cancel := context.WithCancel(uaa.Context)
	defer cancel()
	q := url.Values{}
	q.Set("filter", fmt.Sprintf("email co %q", email))
	fullURL := fmt.Sprintf("%s/Users?%s", uaa.ApiURL, q.Encode())
	logger.Infof("Starting to get all user info: %s\n", fullURL)
	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullURL,
		Method: http.MethodGet,
	}

	respBodyContent, _, err := uaa.Do(&request)
	if err != nil {
		logger.Errorf("Error when getting uaa users by email, %s", err)
		return []UserInfo{}, err
	}
	var document Document
	if err := json.Unmarshal(*respBodyContent, &document); err != nil {
		logger.Errorf("Error when unmarshal uaa users response, %s", err)
		return []UserInfo{}, err
	}
	logger.Infof("Successfully retrieved uaa users: %+v", document)
	users := make([]UserInfo, 0)
	for _, u := range document.Resources {
		for _, em := range u.Emails {
			users = append(users, UserInfo{
				ID:       u.ID,
				Email:    em.Value,
				UserName: u.UserName,
				Groups:   u.Groups,
			})
			break
		}
	}
	return users, nil
}

type UserInfo struct {
	ID       string  `json:"id"`
	Email    string  `json:"email"`
	UserName string  `json:"userName"`
	Groups   []Group `json:"groups"`
}

type Document struct {
	Resources []Resource `json:"resources"`
}

type Resource struct {
	ID                   string    `json:"id"`
	ExternalID           string    `json:"externalId"`
	Meta                 any       `json:"meta"`
	UserName             string    `json:"userName"`
	Name                 Name      `json:"name"`
	Emails               []Email   `json:"emails"`
	Groups               []Group   `json:"groups"`
	Approvals            any       `json:"approvals"`
	Active               bool      `json:"active"`
	Verified             bool      `json:"verified"`
	Origin               string    `json:"origin"`
	ZoneID               string    `json:"zoneId"`
	PasswordLastModified time.Time `json:"passwordLastModified"`
	PreviousLogonTime    int64     `json:"previousLogonTime"`
	LastLogonTime        int64     `json:"lastLogonTime"`
	Schemas              []string  `json:"schemas"`
}

type Name struct {
	FamilyName string `json:"familyName"`
	GivenName  string `json:"givenName"`
}

type Email struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
}

type Group struct {
	Value   string `json:"value"`
	Display string `json:"display"`
	Type    string `json:"type"`
}
