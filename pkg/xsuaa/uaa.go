package xsuaa

import (
	"context"
	"encoding/json"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
	"time"

	"net/http"
	"net/url"
)

type UaaClient struct {
	env.HttpClient
	emailCache map[string]*cacheEntry
}

type cacheEntry struct {
	email     string
	expiresAt time.Time
}

var logger = env.Logger()

const cacheTTL = 30 * time.Hour

var globalClient *UaaClient

func NewClient(c context.Context) (*UaaClient, error) {
	if globalClient != nil {
		return globalClient, nil
	}

	v := env.UaaCredential()
	client, err := env.NewClient(c, v.Clientid, v.Clientsecret, v.AuthUrl, v.ApiUrl)
	if err != nil {
		return nil, err
	}

	globalClient = &UaaClient{
		HttpClient:  *client,
		emailCache:  make(map[string]*cacheEntry),
	}
	return globalClient, nil
}

func GetUserEmail(userID string) (string, error) {
	if globalClient == nil {
		_, err := NewClient(context.Background())
		if err != nil {
			return "", err
		}
	}
	return globalClient.userEmail(userID)
}

// get user by sub/user_id from JWT claim body
func (uaa *UaaClient) UserInfo(userID string) (*db.UserInfo, error) {
	childCtx, cancel := context.WithCancel(uaa.Context)
	defer cancel()
	fullUrl := fmt.Sprintf("%s/Users/%s", uaa.ApiURL, userID)
	logger.Infof("searching user info by sub/user_id, at %s", fullUrl)
	request := env.HttpRequest{
		Ctx:    childCtx,
		ApiURL: fullUrl,
		Method: http.MethodGet,
	}
	body, _, err := uaa.Do(&request)
	if err != nil {
		logger.Errorf("Error when getting uaa user by id, %s", err)
		return nil, err
	}
	var resource Resource
	if err := json.Unmarshal(*body, &resource); err != nil {
		logger.Errorf("Error when unmarshal uaa user response, %s", err)
		return nil, err
	}
	user := db.UserInfo{
		ID:       resource.ID,
		Email:    resource.Emails[0].Value,
		UserName: resource.UserName,
	}
	return &user, nil
}

func (uaa *UaaClient) userEmail(userID string) (string, error) {
	if entry, exists := uaa.emailCache[userID]; exists {
		if time.Now().Before(entry.expiresAt) {
			return entry.email, nil
		}
	}
	userInfo, err := uaa.UserInfo(userID)
	if err != nil {
		return "", err
	}
	uaa.emailCache[userID] = &cacheEntry{
		email:     userInfo.Email,
		expiresAt: time.Now().Add(cacheTTL),
	}
	return userInfo.Email, nil
}

// search uaa user by email, 'co' operator(https://simplecloud.info/specs/draft-scim-api-01.html#query-resources)
func (uaa *UaaClient) SearchByEmail(email string, curUserOrigin string) ([]db.UserInfo, error) {
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
		return []db.UserInfo{}, err
	}
	var document Document
	if err := json.Unmarshal(*respBodyContent, &document); err != nil {
		logger.Errorf("Error when unmarshal uaa users response, %s", err)
		return []db.UserInfo{}, err
	}
	logger.Infof("Successfully retrieved uaa users: %+v", document)
	users := make([]db.UserInfo, 0)
	for _, u := range document.Resources {
		if u.Origin != curUserOrigin {
			continue
		}
		for _, em := range u.Emails {
			users = append(users, db.UserInfo{
				ID:       u.ID,
				Email:    em.Value,
				UserName: u.UserName,
				Origin:   u.Origin,
			})
			break
		}
	}
	return users, nil
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
