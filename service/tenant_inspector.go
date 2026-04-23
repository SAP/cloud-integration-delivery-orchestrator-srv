package service

import (
	"context"
	"fmt"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cf"
)

// ── Bootstrap step names (matches TenantBootstrapJob.CurrentStep values) ─────

const (
	StepCheckTmsSourceNode      = "CHECK_TMS_SOURCE_NODE"
	StepCheckCentralTmsContext  = "CHECK_CENTRAL_TMS_CONTEXT"
	StepCheckSpaceContext       = "CHECK_SPACE_CONTEXT"
	StepCheckDestinationService = "CHECK_DESTINATION_SERVICE"
	StepCheckPirApi             = "CHECK_PIR_API"
	StepCheckCasApplication     = "CHECK_CAS_APPLICATION"
	StepCheckCasStandard        = "CHECK_CAS_STANDARD"
	StepCheckServiceKeys        = "CHECK_SERVICE_KEYS"
	StepCheckDestinations       = "CHECK_DESTINATIONS"
	StepRegisterTmsNode         = "REGISTER_TMS_NODE"
	StepValidateTransport       = "VALIDATE_TRANSPORT_READY"
)

// ── CF service offering / plan constants ──────────────────────────────────────

const (
	offeringPIR         = "it-rt"
	offeringCAS         = "content-agent"
	offeringDestination = "destination"

	planPirApi          = "api"
	planCasApplication  = "application"
	planCasStandard     = "standard"
	planDestinationLite = "lite"
)

// ── cpi-delivery–owned service instance and key names ─────────────────────────
//
// All service instances and persistent service keys created by cpi-delivery
// bootstrap use fixed names so that bootstrap is idempotent and the resources
// are unambiguously owned by cpi-delivery (not accidentally reusing an unrelated
// instance of the same plan that may exist in the subscriber's space).
//
// Instance naming:  cpidelivery-<service>-<plan>-svc
// Key naming:       cpidelivery-<service>-<plan>-key  (persistent keys only;
//                   Destination Service keys are temporary and deleted after use)

const (
	instanceNameDestinationLite = "cpidelivery-dest-lite-svc"
	instanceNamePirApi          = "cpidelivery-pir-api-svc"
	instanceNameCasApplication  = "cpidelivery-cas-app-svc"
	instanceNameCasStandard     = "cpidelivery-cas-std-svc"

	keyNamePirApi         = "cpidelivery-pir-api-key"
	keyNameCasApplication = "cpidelivery-cas-app-key"
	keyNameCasStandard    = "cpidelivery-cas-std-key"
)

// ── Missing-item / label codes ────────────────────────────────────────────────
//
// These codes are used as prefixes for WaitingUserAction / MissingItems entries
// (e.g. missingCodePirApi+"_ENTITLEMENT_MISSING") and as labels in service-key
// checks.

const (
	missingCodeDestinationService = "DESTINATION_SERVICE"
	missingCodePirApi             = "PIR_API"
	missingCodeCasApplication     = "CAS_APPLICATION"
	missingCodeCasStandard        = "CAS_STANDARD"
)

// ── Inspection result types ───────────────────────────────────────────────────

// InspectionResult is the outcome of a read-only InspectTenant call.
// It describes what was found without making any changes to the subscriber
// subaccount.  It drives both preview responses and the initial check phase of
// apply/retry jobs.
type InspectionResult struct {
	// OrgGUID is the CF org GUID derived from the tenant's CfSpace.
	// Resolved during CHECK_SPACE_CONTEXT; required for entitlement queries.
	OrgGUID string

	// SpaceAccessible is true when the CfSpace GUID resolves to a real space.
	SpaceAccessible bool

	// HasSpaceDeveloperRole is true when the operator token's user holds the
	// space_developer role in the CfSpace.
	HasSpaceDeveloperRole bool

	// DestinationServiceEntitled is true when the "destination/lite" plan is
	// visible in the CF org marketplace.
	DestinationServiceEntitled bool

	// DestinationServiceInstanceGUID is set when a destination service instance
	// already exists in the space.  Empty if not yet created.
	DestinationServiceInstanceGUID string

	// PirApiInstanceGUID is set when a PIR api service instance exists.
	PirApiInstanceGUID string

	// CasApplicationInstanceGUID is set when a CAS application service instance exists.
	CasApplicationInstanceGUID string

	// CasStandardInstanceGUID is set when a CAS standard service instance exists.
	CasStandardInstanceGUID string

	// ServiceKeyGUIDs maps instance GUID → service key binding GUID for instances
	// that already have a service key.
	ServiceKeyGUIDs map[string]string

	// DestinationExists maps destination name → true for each of the three required
	// subaccount destinations: CloudIntegration, ContentAssemblyService, TransportManagementService.
	DestinationExists map[string]bool

	// MissingItems lists blocking reason codes for every absent prerequisite.
	// These are written into TenantBootstrapJob.MissingPrerequisites on job creation.
	MissingItems []string

	// PermissionIssues lists items where the bootstrap principal lacks permission.
	PermissionIssues []string

	// WaitingUserAction lists items that require a human to act before bootstrap
	// can continue.  A PIR_API_ENTITLEMENT_MISSING entry implies Integration Suite
	// is not yet subscribed in this subaccount.
	//
	// Space-related codes set by checkSpaceContext:
	//   CF_SPACE_NOT_FOUND      — the CfSpace GUID does not resolve to any space;
	//                             operator must correct the GUID in the tenant record.
	//   CF_TOKEN_UNAUTHORIZED   — the cfToken is expired or invalid;
	//                             operator must re-authenticate and retry.
	WaitingUserAction []string
}

// TenantInspector performs read-only prerequisite checks against a subscriber
// subaccount using the operator-provided CF token.
//
// It is used by:
//   - PreviewBootstrap — the full result drives the preview response.
//   - ApplyBootstrap / RetryBootstrap — the initial check phase before any
//     mutation.
type TenantInspector struct {
	cfClient *cf.CFClient
	userID   string
}

// NewTenantInspector constructs a TenantInspector.
// bearerToken is the short-lived CF token provided by the operator.
// cfAPIURL is the CF API root for the subscriber's BTP region, e.g.
// "https://api.cf.eu10.hana.ondemand.com".
func NewTenantInspector(cfAPIURL, bearerToken string) (*TenantInspector, error) {
	userID, err := cf.ExtractUserID(bearerToken)
	if err != nil {
		return nil, fmt.Errorf("inspector: extract user_id from token: %w", err)
	}
	cfcl, err := cf.NewCFClient(cfAPIURL, bearerToken)
	if err != nil {
		return nil, fmt.Errorf("inspector: create CF client: %w", err)
	}
	return &TenantInspector{cfClient: cfcl, userID: userID}, nil
}

// InspectTenant runs all CHECK_* steps for the given tenant and returns an
// InspectionResult.  It does NOT modify any resources — it is purely read-only.
//
// Step sequence:
//
//	CHECK_SPACE_CONTEXT       → resolve org, verify space accessibility + operator permissions
//	CHECK_DESTINATION_SERVICE → entitlement + instance for the Destination Service jumpboard
//	CHECK_PIR_API             → entitlement (PIR plan visible = IS subscribed) + instance
//	CHECK_CAS_APPLICATION     → entitlement + instance
//	CHECK_CAS_STANDARD        → entitlement + instance
//	CHECK_SERVICE_KEYS        → service key existence per instance
//	CHECK_DESTINATIONS        → CloudIntegration / ContentAssemblyService / TMS destinations
//
// Steps are sequential; early failures (e.g. space not accessible) cause later
// dependent steps to be skipped gracefully.
func (i *TenantInspector) InspectTenant(ctx context.Context, tenant *db.CpiTenant) (*InspectionResult, error) {
	result := &InspectionResult{
		ServiceKeyGUIDs:   make(map[string]string),
		DestinationExists: make(map[string]bool),
	}

	// ── CHECK_SPACE_CONTEXT ──────────────────────────────────────────────────
	//
	// Resolve CF space → org GUID and verify that the operator token holds the
	// space_developer role.  Does NOT check any service instances.
	if err := i.checkSpaceContext(ctx, tenant, result); err != nil {
		return result, fmt.Errorf("inspector: %s: %w", StepCheckSpaceContext, err)
	}
	if !result.SpaceAccessible {
		result.WaitingUserAction = append(result.WaitingUserAction, "CF_SPACE_NOT_ACCESSIBLE")
		return result, nil
	}
	if !result.HasSpaceDeveloperRole {
		result.PermissionIssues = append(result.PermissionIssues, "OPERATOR_MISSING_SPACE_DEVELOPER_ROLE")
		return result, nil
	}

	// ── CHECK_DESTINATION_SERVICE ────────────────────────────────────────────
	//
	// Two-layer check for the Destination Service instance.  This instance is
	// used as the "jumpboard" in CHECK_DESTINATIONS to manage subaccount
	// destinations via the Destination Service REST API.
	if err := i.checkServiceInstance(ctx, result.OrgGUID, tenant.CfSpace, offeringDestination, planDestinationLite,
		instanceNameDestinationLite, missingCodeDestinationService, &result.DestinationServiceInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspector: %s: %w", StepCheckDestinationService, err)
	}

	// ── CHECK_PIR_API ────────────────────────────────────────────────────────
	//
	// Entitlement layer: PIR plan visibility in the CF org marketplace.
	// PIR_API_ENTITLEMENT_MISSING implies Integration Suite is not subscribed.
	if err := i.checkServiceInstance(ctx, result.OrgGUID, tenant.CfSpace, offeringPIR, planPirApi,
		instanceNamePirApi, missingCodePirApi, &result.PirApiInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspector: %s: %w", StepCheckPirApi, err)
	}

	// ── CHECK_CAS_APPLICATION ────────────────────────────────────────────────
	if err := i.checkServiceInstance(ctx, result.OrgGUID, tenant.CfSpace, offeringCAS, planCasApplication,
		instanceNameCasApplication, missingCodeCasApplication, &result.CasApplicationInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspector: %s: %w", StepCheckCasApplication, err)
	}

	// ── CHECK_CAS_STANDARD ───────────────────────────────────────────────────
	if err := i.checkServiceInstance(ctx, result.OrgGUID, tenant.CfSpace, offeringCAS, planCasStandard,
		instanceNameCasStandard, missingCodeCasStandard, &result.CasStandardInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspector: %s: %w", StepCheckCasStandard, err)
	}

	// ── CHECK_SERVICE_KEYS ───────────────────────────────────────────────────
	//
	// For each instance that exists, check whether a service key already exists.
	// Absent keys are noted in MissingItems (apply will create them).
	if err := i.checkServiceKeys(ctx, result); err != nil {
		return result, fmt.Errorf("inspector: %s: %w", StepCheckServiceKeys, err)
	}

	// ── CHECK_DESTINATIONS ───────────────────────────────────────────────────
	//
	// Check the three required subaccount destinations using a temporary service
	// key on the Destination Service instance.
	if result.DestinationServiceInstanceGUID != "" {
		if err := i.checkDestinations(ctx, result); err != nil {
			return result, fmt.Errorf("inspector: %s: %w", StepCheckDestinations, err)
		}
	}

	return result, nil
}

// ── Step implementations ──────────────────────────────────────────────────────

// checkSpaceContext resolves the CF space and its parent org GUID, and verifies
// that the operator holds the space_developer role.  It does NOT check any
// service instances — that is delegated to checkServiceInstance calls in
// InspectTenant.
func (i *TenantInspector) checkSpaceContext(ctx context.Context, tenant *db.CpiTenant, result *InspectionResult) error {
	if tenant.CfSpace == "" {
		result.MissingItems = append(result.MissingItems, "CF_SPACE_NOT_CONFIGURED")
		return nil
	}

	// Confirm the space is accessible and resolve the parent org GUID.
	space, err := i.cfClient.GetSpace(ctx, tenant.CfSpace)
	if err != nil {
		switch cf.HTTPStatusCode(err) {
		case 404:
			// Space GUID does not exist: operator must correct the value in the
			// tenant record and re-run the wizard.
			result.WaitingUserAction = append(result.WaitingUserAction, "CF_SPACE_NOT_FOUND")
			return nil
		case 401, 403:
			// Token is expired or lacks permission: operator must re-authenticate
			// and provide a fresh cfToken.
			result.WaitingUserAction = append(result.WaitingUserAction, "CF_TOKEN_UNAUTHORIZED")
			return nil
		default:
			// Network error, 5xx, or other unexpected failure: not an operator
			// configuration issue — propagate so the job ends as REMOTE_SYSTEM_ERROR.
			return fmt.Errorf("get space %s: %w", tenant.CfSpace, err)
		}
	}
	result.SpaceAccessible = true

	if space.Relationships.Organization != nil && space.Relationships.Organization.Data != nil {
		result.OrgGUID = space.Relationships.Organization.Data.GUID
	}

	// Verify operator permission.
	hasDev, err := i.cfClient.HasSpaceDeveloperRole(ctx, tenant.CfSpace, i.userID)
	if err != nil {
		switch cf.HTTPStatusCode(err) {
		case 401, 403:
			// Token expired between GetSpace and role check; treat the same as a
			// token failure on GetSpace.
			result.WaitingUserAction = append(result.WaitingUserAction, "CF_TOKEN_UNAUTHORIZED")
			return nil
		default:
			return fmt.Errorf("roles check: %w", err)
		}
	}
	result.HasSpaceDeveloperRole = hasDev
	return nil
}

// checkServiceInstance performs the two-layer check (entitlement → instance)
// for a single service instance type and updates result accordingly.
//
// instanceName is the fixed name under which cpi-delivery creates and looks up
// its dedicated instance (e.g. "cpidelivery-pir-api-svc").  Lookup is by name,
// not by plan, so that an unrelated instance of the same plan in the space is
// never accidentally reused.
//
// missingCode is the prefix used in error codes (e.g. "PIR_API", "CAS_APPLICATION").
// Entitlement failures append "<missingCode>_ENTITLEMENT_MISSING" to WaitingUserAction.
// Instance absence appends "<missingCode>_INSTANCE_MISSING" to MissingItems.
//
// For PIR_API: entitlement failure also implies Integration Suite is not subscribed.
func (i *TenantInspector) checkServiceInstance(
	ctx context.Context,
	orgGUID, spaceGUID string,
	offering, plan string,
	instanceName string,
	missingCode string,
	instanceGUIDOut *string,
	result *InspectionResult,
) error {
	// Layer 1 — entitlement (service plan visibility in CF org marketplace).
	if orgGUID != "" {
		entitled, err := i.cfClient.IsServicePlanVisible(ctx, orgGUID, offering, plan)
		if err != nil {
			return fmt.Errorf("entitlement check (offering=%s, plan=%s): %w", offering, plan, err)
		}
		if !entitled {
			result.WaitingUserAction = append(result.WaitingUserAction,
				missingCode+"_ENTITLEMENT_MISSING")
			return nil
		}
	}

	// Layer 2 — instance existence: look up by the cpi-delivery–owned name only.
	// Using GetServiceInstanceByName instead of GetServiceInstance(plan) ensures
	// we never accidentally reuse an unrelated instance of the same plan.
	instance, err := i.cfClient.GetServiceInstanceByName(ctx, spaceGUID, instanceName)
	if err != nil {
		return fmt.Errorf("instance check (name=%s): %w", instanceName, err)
	}
	if instance == nil {
		result.MissingItems = append(result.MissingItems, missingCode+"_INSTANCE_MISSING")
		return nil
	}
	*instanceGUIDOut = instance.GUID
	return nil
}

// checkServiceKeys checks whether each existing service instance already has a
// service key, and records the binding GUID when found.
func (i *TenantInspector) checkServiceKeys(ctx context.Context, result *InspectionResult) error {
	check := func(instanceGUID, label string) error {
		if instanceGUID == "" {
			return nil // instance doesn't exist yet; key check is irrelevant
		}
		binding, err := i.cfClient.GetServiceKey(ctx, instanceGUID)
		if err != nil {
			return fmt.Errorf("service key check (%s): %w", label, err)
		}
		if binding != nil {
			result.ServiceKeyGUIDs[instanceGUID] = binding.GUID
		} else {
			result.MissingItems = append(result.MissingItems, label+"_SERVICE_KEY_MISSING")
		}
		return nil
	}

	if err := check(result.PirApiInstanceGUID, missingCodePirApi); err != nil {
		return err
	}
	if err := check(result.CasApplicationInstanceGUID, missingCodeCasApplication); err != nil {
		return err
	}
	if err := check(result.CasStandardInstanceGUID, missingCodeCasStandard); err != nil {
		return err
	}
	return nil
}

// checkDestinations uses a temporary service key on the Destination Service
// instance to list existing subaccount destinations and checks for the three
// required ones.
//
// The temporary key is created and deleted within this function — it never
// outlives the inspection call.
func (i *TenantInspector) checkDestinations(ctx context.Context, result *InspectionResult) error {
	destInstanceGUID := result.DestinationServiceInstanceGUID
	if destInstanceGUID == "" {
		return nil
	}

	// Create a temporary service key to obtain Destination Service credentials.
	tempKeyName := fmt.Sprintf("cpidelivery-inspect-%d", time.Now().Unix())
	keyGUID, err := i.cfClient.CreateServiceKey(ctx, destInstanceGUID, tempKeyName)
	if err != nil {
		return fmt.Errorf("create temp service key for destination check: %w", err)
	}
	defer func() {
		// Best-effort cleanup — log failure but do not surface it as an error.
		_ = i.cfClient.DeleteServiceKey(ctx, keyGUID)
	}()

	creds, err := i.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
	if err != nil {
		return fmt.Errorf("get temp service key credentials: %w", err)
	}

	destClient, err := cf.NewDestinationServiceClient(ctx, creds)
	if err != nil {
		return fmt.Errorf("build destination service client: %w", err)
	}

	existing, err := destClient.ListDestinations(ctx)
	if err != nil {
		return fmt.Errorf("list destinations: %w", err)
	}

	byName := make(map[string]bool, len(existing))
	for _, d := range existing {
		byName[d.Name] = true
	}

	required := []string{
		"CloudIntegration",
		"ContentAssemblyService",
		"TransportManagementService",
	}
	for _, name := range required {
		result.DestinationExists[name] = byName[name]
		if !byName[name] {
			result.MissingItems = append(result.MissingItems, name+"_DESTINATION_MISSING")
		}
	}
	return nil
}
