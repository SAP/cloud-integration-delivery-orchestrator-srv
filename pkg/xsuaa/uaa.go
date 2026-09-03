package xsuaa

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/consts"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
	"time"

	"net/http"
	"net/url"

	"go.uber.org/zap"
)

type UaaClient struct {
	*env.HttpClient
	emailCache map[string]*cacheEntry
}

type cacheEntry struct {
	email     string
	expiresAt time.Time
}

// logger returns a context-aware logger that includes trace_id/span_id when OTel is active.
func logger(ctx context.Context) *zap.SugaredLogger { return env.L(ctx) }

const cacheTTL = 30 * time.Hour

var globalClient *UaaClient

func NewClient(ctx context.Context) (*UaaClient, error) {
	if globalClient != nil {
		return globalClient, nil
	}

	v := env.ApiUaaCredential()
	client, err := env.NewClient(ctx, v.Clientid, v.Clientsecret, v.AuthUrl, v.ApiUrl)
	if err != nil {
		return nil, err
	}

	globalClient = &UaaClient{
		HttpClient: client,
		emailCache: make(map[string]*cacheEntry),
	}
	return globalClient, nil
}

func GetUserEmail(ctx context.Context, userID string) (string, error) {
	if globalClient == nil {
		_, err := NewClient(ctx)
		if err != nil {
			return "", err
		}
	}
	return globalClient.UserEmail(ctx, userID)
}

// get user by sub/user_id from JWT claim body
func (uaa *UaaClient) UserInfo(ctx context.Context, userID string) (*db.UserInfo, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	fullUrl := fmt.Sprintf("%s/Users/%s", uaa.ApiURL, userID)
	logger(ctx).Infow("searching user info by sub/user_id", "url", fullUrl)
	request := env.HttpRequest{
		ApiURL: fullUrl,
		Method: http.MethodGet,
	}
	body, err := uaa.Do(childCtx, &request)
	if err != nil {
		return nil, fmt.Errorf("UserInfo: %w", err)
	}
	var resource Resource
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, fmt.Errorf("UserInfo: unmarshal: %w", err)
	}
	user := db.UserInfo{
		ID:       resource.ID,
		Email:    resource.Emails[0].Value,
		UserName: resource.UserName,
	}
	return &user, nil
}

func (uaa *UaaClient) UserEmail(ctx context.Context, userID string) (string, error) {
	if entry, exists := uaa.emailCache[userID]; exists {
		if time.Now().Before(entry.expiresAt) {
			return entry.email, nil
		}
	}
	userInfo, err := uaa.UserInfo(ctx, userID)
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
func (uaa *UaaClient) SearchByEmail(ctx context.Context, email string, curUserOrigin string) ([]db.UserInfo, error) {
	childCtx, cancel := context.WithTimeout(ctx, consts.DefaultRequestTimeout)
	defer cancel()
	q := url.Values{}
	q.Set("filter", fmt.Sprintf("email co %q", email))
	fullURL := fmt.Sprintf("%s/Users?%s", uaa.ApiURL, q.Encode())
	logger(ctx).Infow("searching users by email", "url", fullURL)
	request := env.HttpRequest{
		ApiURL: fullURL,
		Method: http.MethodGet,
	}

	respBodyContent, err := uaa.Do(childCtx, &request)
	if err != nil {
		return []db.UserInfo{}, fmt.Errorf("SearchByEmail: %w", err)
	}
	var document Document
	if err := json.Unmarshal(respBodyContent, &document); err != nil {
		return []db.UserInfo{}, fmt.Errorf("SearchByEmail: unmarshal: %w", err)
	}
	logger(ctx).Infow("successfully retrieved uaa users", "document", document)
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
