// Package cf provides a thin wrapper around the go-cfclient/v3 SDK for
// performing Cloud Foundry API operations during tenant bootstrap.
//
// All bootstrap operations against a subscriber subaccount can be done purely
// via CF API using a short-lived CF Bearer token provided by the operator at
// apply/retry time.  No BTP platform layer (Service Manager, Accounts API) is
// needed.
//
// The CFClient lifetime equals one bootstrap job execution.  The operator
// token is held only in goroutine memory and is never written to the database
// or any persistent store.
package cf

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudfoundry/go-cfclient/v3/client"
	"github.com/cloudfoundry/go-cfclient/v3/config"
	"github.com/cloudfoundry/go-cfclient/v3/resource"
)

// CFClient wraps the go-cfclient/v3 SDK client and exposes the subset of CF
// API operations required by the tenant bootstrap inspector and bootstrapper.
type CFClient struct {
	inner *client.Client
}

// NewCFClient constructs a CFClient authenticated with a short-lived Bearer
// token supplied by the operator.  The token is used only for the duration of
// one bootstrap job; it is never persisted.
//
// apiURL is the CF API root, e.g. "https://api.cf.eu10.hana.ondemand.com".
// bearerToken is the raw token value (without the "Bearer " prefix).
func NewCFClient(apiURL, bearerToken string) (*CFClient, error) {
	cfg, err := config.New(
		apiURL,
		config.Token(bearerToken, "" /* refresh token unused for short-lived operator token */),
	)
	if err != nil {
		return nil, fmt.Errorf("cf: build config: %w", err)
	}

	inner, err := client.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("cf: create client: %w", err)
	}
	return &CFClient{inner: inner}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Identity helpers
// ──────────────────────────────────────────────────────────────────────────────

// ExtractUserID decodes the CF JWT token payload (without signature verification —
// the CF API will reject invalid tokens on first use) and returns the user_id claim.
// This is a local operation: zero API calls.
func ExtractUserID(bearerToken string) (string, error) {
	parts := strings.Split(bearerToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("cf: token does not look like a JWT (expected 3 parts, got %d)", len(parts))
	}
	// JWT uses base64url encoding without padding.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("cf: base64 decode JWT payload: %w", err)
	}
	var claims struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("cf: unmarshal JWT claims: %w", err)
	}
	if claims.UserID == "" {
		return "", fmt.Errorf("cf: user_id claim is empty in token")
	}
	return claims.UserID, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Entitlement check — service plan visibility in CF org marketplace
// ──────────────────────────────────────────────────────────────────────────────

// IsServicePlanVisible returns true if the given service plan (identified by
// offering name + plan name) is visible in the CF org marketplace.
//
// Entitlement signal: Integration Suite is a BTP SaaS subscription.  When
// subscribed, BTP injects the corresponding service offerings (e.g.
// "process-integration-runtime"(it-rt), "content-agent") into the CF org marketplace.
// Querying plan visibility is therefore the correct indirect way to verify that
// Integration Suite has been subscribed.
//
// API: GET /v3/service_plans?service_offering_names=<offering>&organization_guids=<org>
func (c *CFClient) IsServicePlanVisible(ctx context.Context, orgGUID, serviceOfferingName, planName string) (bool, error) {
	opts := client.NewServicePlanListOptions()
	opts.ServiceOfferingNames = client.Filter{Values: []string{serviceOfferingName}}
	opts.OrganizationGUIDs = client.Filter{Values: []string{orgGUID}}
	if planName != "" {
		opts.Names = client.Filter{Values: []string{planName}}
	}

	plans, err := c.inner.ServicePlans.ListAll(ctx, opts)
	if err != nil {
		return false, fmt.Errorf("cf: list service plans (offering=%s, plan=%s): %w", serviceOfferingName, planName, err)
	}
	return len(plans) > 0, nil
}

// GetServicePlanGUID returns the GUID of a specific service plan (offering + plan name)
// visible in a CF org.  Returns an error if not found.
func (c *CFClient) GetServicePlanGUID(ctx context.Context, orgGUID, offeringName, planName string) (string, error) {
	opts := client.NewServicePlanListOptions()
	opts.ServiceOfferingNames = client.Filter{Values: []string{offeringName}}
	opts.Names = client.Filter{Values: []string{planName}}
	opts.OrganizationGUIDs = client.Filter{Values: []string{orgGUID}}

	plans, err := c.inner.ServicePlans.ListAll(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("cf: get service plan GUID (offering=%s, plan=%s): %w", offeringName, planName, err)
	}
	if len(plans) == 0 {
		return "", fmt.Errorf("cf: service plan not found (offering=%s, plan=%s)", offeringName, planName)
	}
	return plans[0].GUID, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Service instance operations
// ──────────────────────────────────────────────────────────────────────────────

// GetServiceInstance returns the first managed service instance in the given
// space that matches the specified service plan name.  Returns nil, nil when no
// matching instance is found.
//
// API: GET /v3/service_instances?space_guids=<space>&service_plan_names=<plan>
func (c *CFClient) GetServiceInstance(ctx context.Context, spaceGUID, planName string) (*resource.ServiceInstance, error) {
	opts := client.NewServiceInstanceListOptions()
	opts.SpaceGUIDs = client.Filter{Values: []string{spaceGUID}}
	if planName != "" {
		opts.ServicePlanNames = client.Filter{Values: []string{planName}}
	}

	instances, err := c.inner.ServiceInstances.ListAll(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("cf: list service instances (plan=%s): %w", planName, err)
	}
	if len(instances) == 0 {
		return nil, nil
	}
	return instances[0], nil
}

// GetServiceInstanceByName returns a service instance matching both space and name.
func (c *CFClient) GetServiceInstanceByName(ctx context.Context, spaceGUID, instanceName string) (*resource.ServiceInstance, error) {
	opts := client.NewServiceInstanceListOptions()
	opts.SpaceGUIDs = client.Filter{Values: []string{spaceGUID}}
	opts.Names = client.Filter{Values: []string{instanceName}}

	instances, err := c.inner.ServiceInstances.ListAll(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("cf: get service instance by name %q: %w", instanceName, err)
	}
	if len(instances) == 0 {
		return nil, nil
	}
	return instances[0], nil
}

// CreateManagedServiceInstance creates a new managed service instance and polls
// until the async CF job completes.  Returns the created instance GUID.
//
// API: POST /v3/service_instances  →  jobGUID  →  GET /v3/jobs/{guid} (poll)
func (c *CFClient) CreateManagedServiceInstance(ctx context.Context, spaceGUID, planGUID, instanceName string) (string, error) {
	req := resource.NewServiceInstanceCreateManaged(instanceName, spaceGUID, planGUID)

	jobGUID, err := c.inner.ServiceInstances.CreateManaged(ctx, req)
	if err != nil {
		return "", fmt.Errorf("cf: create service instance %q: %w", instanceName, err)
	}

	if err := c.inner.Jobs.PollComplete(ctx, jobGUID, nil); err != nil {
		return "", fmt.Errorf("cf: poll create service instance %q: %w", instanceName, err)
	}

	instance, err := c.GetServiceInstanceByName(ctx, spaceGUID, instanceName)
	if err != nil || instance == nil {
		return "", fmt.Errorf("cf: locate created instance %q after creation", instanceName)
	}
	return instance.GUID, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Service credential binding (service key) operations
// ──────────────────────────────────────────────────────────────────────────────

// GetServiceKey returns the first credential binding (service key) for the
// given service instance.  Returns nil, nil when none exists.
//
// API: GET /v3/service_credential_bindings?service_instance_guids=<guid>
func (c *CFClient) GetServiceKey(ctx context.Context, instanceGUID string) (*resource.ServiceCredentialBinding, error) {
	opts := client.NewServiceCredentialBindingListOptions()
	opts.ServiceInstanceGUIDs = client.Filter{Values: []string{instanceGUID}}

	bindings, err := c.inner.ServiceCredentialBindings.ListAll(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("cf: list service keys (instance=%s): %w", instanceGUID, err)
	}
	// Filter to key type in memory (the API types filter uses a different field name in this version).
	for _, b := range bindings {
		if b.Type == "key" {
			return b, nil
		}
	}
	return nil, nil
}

// CreateServiceKey creates a new service key for the given service instance.
// Returns the binding GUID and polls until the async job completes.
//
// API: POST /v3/service_credential_bindings  →  jobGUID  →  GET /v3/jobs/{guid} (poll)
func (c *CFClient) CreateServiceKey(ctx context.Context, instanceGUID, keyName string) (string, error) {
	req := resource.NewServiceCredentialBindingCreateKey(instanceGUID, keyName)

	jobGUID, _, err := c.inner.ServiceCredentialBindings.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("cf: create service key %q: %w", keyName, err)
	}

	if err := c.inner.Jobs.PollComplete(ctx, jobGUID, nil); err != nil {
		return "", fmt.Errorf("cf: poll create service key %q: %w", keyName, err)
	}

	// Locate the created binding by name.
	opts := client.NewServiceCredentialBindingListOptions()
	opts.ServiceInstanceGUIDs = client.Filter{Values: []string{instanceGUID}}
	opts.Names = client.Filter{Values: []string{keyName}}
	bindings, err := c.inner.ServiceCredentialBindings.ListAll(ctx, opts)
	if err != nil || len(bindings) == 0 {
		return "", fmt.Errorf("cf: locate created service key %q", keyName)
	}
	return bindings[0].GUID, nil
}

// GetServiceKeyCredentials returns the raw credential map for a service key.
//
// API: GET /v3/service_credential_bindings/{guid}/details
func (c *CFClient) GetServiceKeyCredentials(ctx context.Context, bindingGUID string) (map[string]any, error) {
	details, err := c.inner.ServiceCredentialBindings.GetDetails(ctx, bindingGUID)
	if err != nil {
		return nil, fmt.Errorf("cf: get service key details (%s): %w", bindingGUID, err)
	}
	return details.Credentials, nil
}

// DeleteServiceKey deletes a service key and polls until the async job completes.
//
// API: DELETE /v3/service_credential_bindings/{guid}  →  jobGUID  →  GET /v3/jobs/{guid} (poll)
func (c *CFClient) DeleteServiceKey(ctx context.Context, bindingGUID string) error {
	jobGUID, err := c.inner.ServiceCredentialBindings.Delete(ctx, bindingGUID)
	if err != nil {
		return fmt.Errorf("cf: delete service key (%s): %w", bindingGUID, err)
	}
	if err := c.inner.Jobs.PollComplete(ctx, jobGUID, nil); err != nil {
		return fmt.Errorf("cf: poll delete service key (%s): %w", bindingGUID, err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// RBAC / permission check
// ──────────────────────────────────────────────────────────────────────────────

// HasSpaceDeveloperRole returns true if userID holds the space_developer role
// in the given CF space.
//
// API: GET /v3/roles?types=space_developer&space_guids=<space>&user_guids=<user>
func (c *CFClient) HasSpaceDeveloperRole(ctx context.Context, spaceGUID, userID string) (bool, error) {
	opts := client.NewRoleListOptions()
	opts.WithSpaceRoleType(resource.SpaceRoleDeveloper)
	opts.SpaceGUIDs = client.Filter{Values: []string{spaceGUID}}
	opts.UserGUIDs = client.Filter{Values: []string{userID}}

	roles, err := c.inner.Roles.ListAll(ctx, opts)
	if err != nil {
		return false, fmt.Errorf("cf: list roles (space=%s, user=%s): %w", spaceGUID, userID, err)
	}
	return len(roles) > 0, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Space helpers
// ──────────────────────────────────────────────────────────────────────────────

// GetSpace returns the CF space resource for the given space GUID.
//
// API: GET /v3/spaces/{guid}
func (c *CFClient) GetSpace(ctx context.Context, spaceGUID string) (*resource.Space, error) {
	space, err := c.inner.Spaces.Get(ctx, spaceGUID)
	if err != nil {
		return nil, fmt.Errorf("cf: get space (%s): %w", spaceGUID, err)
	}
	return space, nil
}

// GetOrgForSpace returns the GUID of the CF organization that owns the given space.
func (c *CFClient) GetOrgForSpace(ctx context.Context, spaceGUID string) (string, error) {
	space, err := c.GetSpace(ctx, spaceGUID)
	if err != nil {
		return "", err
	}
	if space.Relationships.Organization == nil || space.Relationships.Organization.Data == nil {
		return "", fmt.Errorf("cf: space %s has no organization relationship", spaceGUID)
	}
	return space.Relationships.Organization.Data.GUID, nil
}
