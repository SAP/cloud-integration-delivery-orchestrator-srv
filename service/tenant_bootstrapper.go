package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"
	"mmt-delivery/pkg/lifecycle"
)

// ── Public service entry points ───────────────────────────────────────────────

// PreviewBootstrap runs a read-only inspection of the tenant's local
// prerequisites and returns a BootstrapPreview that describes what is present,
// what is missing, and what would be created by ApplyBootstrap.
//
// It does NOT create a TenantBootstrapJob row — preview is lightweight and
// read-only.  The result is returned directly to the caller (handler).
//
// No cfToken is required because InspectTenant is the only operation called.
// Wait — preview still needs a cfToken to call CF API for the inspection.
func (s *Service) PreviewBootstrap(ctx context.Context, tenantID uint, cfToken string) (*BootstrapPreview, error) {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return nil, err
	}

	inspector, err := newInspector(tenant, cfToken)
	if err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}

	result, err := inspector.InspectTenant(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("preview: inspect: %w", err)
	}

	return buildPreview(tenant, result), nil
}

// ApplyBootstrap synchronously inspects the tenant's prerequisites, then
// creates a new "apply" TenantBootstrapJob and launches the apply goroutine.
// Returns the created job ID so the caller can poll GetBootstrapStatus.
//
// Inspection is run synchronously so that permission blocks and waiting-user-action
// items are surfaced immediately in the HTTP response rather than requiring the
// caller to poll job status.  Only if inspection passes (no blocking issues) is
// the goroutine launched.
//
// cfToken is the short-lived CF Bearer token provided by the Operator.  It is
// forwarded to the goroutine and never persisted.
func (s *Service) ApplyBootstrap(ctx context.Context, tenantID uint, cfToken string) (uint, error) {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return 0, err
	}

	// Guard: reject if a bootstrap job is already in progress.
	// A job row is created below; we must confirm no other goroutine owns the
	// tenant before writing it, otherwise a concurrent Apply call would produce
	// an orphaned job row (state=running, ended_at=null) that never completes.
	if tenant.LifecycleState == lifecycle.TenantReadying {
		return 0, fmt.Errorf("apply: a bootstrap job is already running for tenant %d", tenantID)
	}

	// ── Synchronous inspect phase ────────────────────────────────────────────
	inspector, err := newInspector(tenant, cfToken)
	if err != nil {
		return 0, fmt.Errorf("apply: build inspector: %w", err)
	}
	result, err := inspector.InspectTenant(ctx, tenant)
	if err != nil {
		return 0, fmt.Errorf("apply: inspect: %w", err)
	}
	if len(result.PermissionIssues) > 0 {
		return 0, fmt.Errorf("apply: permission issues: %v", result.PermissionIssues)
	}
	if len(result.WaitingUserAction) > 0 {
		return 0, fmt.Errorf("apply: waiting user action: %v", result.WaitingUserAction)
	}

	// ── Create job row and transition state ──────────────────────────────────
	missingJSON, _ := json.Marshal(result.MissingItems)
	job := &db.TenantBootstrapJob{
		CpiTenantID:          tenantID,
		JobType:              lifecycle.JobTypeApply,
		State:                lifecycle.JobRunning,
		MissingPrerequisites: missingJSON,
		StartedAt:            time.Now(),
	}
	if err := s.DB.Create(job).Error; err != nil {
		return 0, fmt.Errorf("apply: create job: %w", err)
	}

	if err := s.TransitionLifecycle(tenantID, EventBootstrapStarted); err != nil {
		return 0, fmt.Errorf("apply: transition state: %w", err)
	}

	go s.runBootstrap(tenant, job.ID, cfToken, result)
	return job.ID, nil
}

// RetryBootstrap synchronously inspects the tenant's prerequisites, then
// creates a new "retry" TenantBootstrapJob that continues from where the last
// apply/retry job failed.  Like ApplyBootstrap, inspection runs synchronously
// and the apply phase runs asynchronously.
//
// cfToken must be provided again (short-lived tokens are not stored anywhere).
func (s *Service) RetryBootstrap(ctx context.Context, tenantID uint, cfToken string) (uint, error) {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return 0, err
	}

	// Only permit retry when the tenant is not_ready (last job stopped).
	if tenant.LifecycleState == lifecycle.TenantReadying {
		return 0, fmt.Errorf("retry: a bootstrap job is already running for tenant %d", tenantID)
	}
	if tenant.LifecycleState == lifecycle.TenantReady {
		return 0, fmt.Errorf("retry: tenant %d is already ready", tenantID)
	}

	// ── Synchronous inspect phase ────────────────────────────────────────────
	inspector, err := newInspector(tenant, cfToken)
	if err != nil {
		return 0, fmt.Errorf("retry: build inspector: %w", err)
	}
	result, err := inspector.InspectTenant(ctx, tenant)
	if err != nil {
		return 0, fmt.Errorf("retry: inspect: %w", err)
	}
	if len(result.PermissionIssues) > 0 {
		return 0, fmt.Errorf("retry: permission issues: %v", result.PermissionIssues)
	}
	if len(result.WaitingUserAction) > 0 {
		return 0, fmt.Errorf("retry: waiting user action: %v", result.WaitingUserAction)
	}

	// ── Create job row and transition state ──────────────────────────────────
	missingJSON, _ := json.Marshal(result.MissingItems)
	job := &db.TenantBootstrapJob{
		CpiTenantID:          tenantID,
		JobType:              lifecycle.JobTypeRetry,
		State:                lifecycle.JobRunning,
		MissingPrerequisites: missingJSON,
		StartedAt:            time.Now(),
	}
	if err := s.DB.Create(job).Error; err != nil {
		return 0, fmt.Errorf("retry: create job: %w", err)
	}

	if err := s.TransitionLifecycle(tenantID, EventBootstrapStarted); err != nil {
		return 0, fmt.Errorf("retry: transition state: %w", err)
	}

	go s.runBootstrap(tenant, job.ID, cfToken, result)
	return job.ID, nil
}

// GetBootstrapStatus returns the most recent TenantBootstrapJob for the tenant.
func (s *Service) GetBootstrapStatus(tenantID uint) (*db.TenantBootstrapJob, error) {
	var job db.TenantBootstrapJob
	err := s.DB.Where("cpi_tenant_id = ?", tenantID).
		Order("created_at DESC").
		First(&job).Error
	if err != nil {
		return nil, fmt.Errorf("bootstrap status: %w", err)
	}
	return &job, nil
}

// ResetBootstrap is an operator escape hatch for tenants stuck in the readying
// state due to an irrecoverable goroutine failure (e.g. DB unavailable when
// runBootstrap tried to write its final lifecycle transition).
//
// The operator calls this endpoint after observing that the tenant has been in
// readying for longer than expected.  The reset marks the active running job as
// failed and transitions the tenant back to not_ready, after which a normal
// RetryBootstrap can be issued.
//
// POST /api/v1/cpiTenant/:id/bootstrap/reset
func (s *Service) ResetBootstrap(tenantID uint) error {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return err
	}

	if tenant.LifecycleState != lifecycle.TenantReadying {
		return fmt.Errorf("reset: tenant %d is not in readying state (current: %s); nothing to reset",
			tenantID, tenant.LifecycleState)
	}

	// Mark the most recent running job as failed.
	now := time.Now()
	if err := s.DB.Model(&db.TenantBootstrapJob{}).
		Where("cpi_tenant_id = ? AND state = ?", tenantID, lifecycle.JobRunning).
		Updates(map[string]any{
			"state":        lifecycle.JobFailed,
			"failure_type": lifecycle.FailureRemoteSystemError,
			"ended_at":     &now,
		}).Error; err != nil {
		return fmt.Errorf("reset: mark job failed: %w", err)
	}

	// Transition tenant back to not_ready so RetryBootstrap can proceed.
	if err := s.TransitionLifecycle(tenantID, EventBootstrapFailed); err != nil {
		return fmt.Errorf("reset: transition lifecycle: %w", err)
	}
	return nil
}

// ── BootstrapPreview ──────────────────────────────────────────────────────────

// BootstrapPreview is the read-only result of a PreviewBootstrap call.
// It describes what was found in the subscriber subaccount and what would
// happen during an apply.  Callers inspect InspectionResult.MissingItems for
// the list of resources that ApplyBootstrap would create.
type BootstrapPreview struct {
	TenantID         uint              `json:"tenantId"`
	InspectionResult *InspectionResult `json:"inspection"`
}

func buildPreview(tenant *db.CpiTenant, result *InspectionResult) *BootstrapPreview {
	return &BootstrapPreview{
		TenantID:         tenant.ID,
		InspectionResult: result,
	}
}

// ── Bootstrap goroutine ───────────────────────────────────────────────────────

// runBootstrap is the async apply phase.  It receives the InspectionResult
// already produced by the synchronous inspect phase in ApplyBootstrap or
// RetryBootstrap — it does NOT re-run InspectTenant.
//
// runBootstrap must NOT be called directly — use ApplyBootstrap or RetryBootstrap.
func (s *Service) runBootstrap(tenant *db.CpiTenant, jobID uint, cfToken string, result *InspectionResult) {
	ctx := context.Background()

	fail := func(failureType lifecycle.BootstrapFailureType, step, reason string) {
		state := lifecycle.JobFailed
		if failureType == lifecycle.FailureWaitingUserAction {
			state = lifecycle.JobWaitingUserAction
		}
		now := time.Now()
		s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).Updates(map[string]any{
			"state":        state,
			"failure_type": failureType,
			"current_step": step,
			"ended_at":     &now,
			"error_detail": reason,
		})
		if err := s.TransitionLifecycle(tenant.ID, EventBootstrapFailed); err != nil {
			env.Logger().Errorw("bootstrap: failed to transition lifecycle after job failure; tenant may be stuck in readying",
				"tenantID", tenant.ID, "jobID", jobID, "error", err)
		}
		if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
			Update("blocking_reason", reason).Error; err != nil {
			env.Logger().Errorw("bootstrap: failed to persist blocking_reason",
				"tenantID", tenant.ID, "jobID", jobID, "error", err)
		}
	}

	finish := func() {
		now := time.Now()
		s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).Updates(map[string]any{
			"state":    lifecycle.JobFinished,
			"ended_at": &now,
		})
		if err := s.TransitionLifecycle(tenant.ID, EventBootstrapFinished); err != nil {
			env.Logger().Errorw("bootstrap: failed to transition lifecycle after job completion; tenant may be stuck in readying",
				"tenantID", tenant.ID, "jobID", jobID, "error", err)
		}
	}

	setStep := func(step string) {
		s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
			Update("current_step", step)
	}

	// ── Apply missing items ───────────────────────────────────────────────────

	bootstrapper, err := newBootstrapApplier(tenant, cfToken, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, "", fmt.Sprintf("build applier: %s", err))
		return
	}

	var credentialActions []credentialAction

	// CHECK_PIR_API → create instance + service key if missing
	setStep(StepCheckPirApi)
	acts, err := bootstrapper.ensureInstanceAndKey(ctx, offeringPIR, planPirApi,
		instanceNamePirApi, keyNamePirApi,
		missingCodePirApi, &result.PirApiInstanceGUID, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckPirApi, err.Error())
		return
	}
	credentialActions = append(credentialActions, acts...)

	// CHECK_CAS_APPLICATION → create instance + service key if missing.
	// Note: the CAS application service key is NOT used to build a subscriber-side
	// destination here.  It will be consumed by TrResolver (Phase 4) via the
	// provider-side CPIDELIVERY_CAS_{id} destination.
	setStep(StepCheckCasApplication)
	acts, err = bootstrapper.ensureInstanceAndKey(ctx, offeringCAS, planCasApplication,
		instanceNameCasApplication, keyNameCasApplication,
		missingCodeCasApplication, &result.CasApplicationInstanceGUID, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckCasApplication, err.Error())
		return
	}
	credentialActions = append(credentialActions, acts...)

	// CHECK_CAS_STANDARD → create instance + service key if missing
	setStep(StepCheckCasStandard)
	acts, err = bootstrapper.ensureInstanceAndKey(ctx, offeringCAS, planCasStandard,
		instanceNameCasStandard, keyNameCasStandard,
		missingCodeCasStandard, &result.CasStandardInstanceGUID, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckCasStandard, err.Error())
		return
	}
	credentialActions = append(credentialActions, acts...)

	// CHECK_DESTINATION_SERVICE → create instance if missing.
	// Grouped with CHECK_DESTINATIONS: the instance is the vehicle for writing
	// the destination configs that depend on the PIR/CAS credentials above.
	//
	// Unlike PIR/CAS, the Destination Service does NOT need a persistent service
	// key managed here.  ensureDestinations creates a short-lived temporary key
	// (deleted via defer) solely to call the Destination Service REST API, so
	// ensureInstanceAndKey is not used for this service.
	setStep(StepCheckDestinationService)
	if result.DestinationServiceInstanceGUID == "" {
		guid, err := bootstrapper.ensureServiceInstance(ctx, offeringDestination, planDestinationLite,
			instanceNameDestinationLite)
		if err != nil {
			fail(lifecycle.FailureRemoteSystemError, StepCheckDestinationService, err.Error())
			return
		}
		result.DestinationServiceInstanceGUID = guid
	}

	// CHECK_DESTINATIONS → write all required destinations into the Destination Service instance
	setStep(StepCheckDestinations)
	destActs, err := bootstrapper.ensureDestinations(ctx, tenant, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckDestinations, err.Error())
		return
	}
	credentialActions = append(credentialActions, destActs...)

	// Persist credential action log (no secrets — names and action types only).
	actsJSON, _ := json.Marshal(credentialActions)
	s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
		Update("credential_actions", actsJSON)

	// Persist updated destination names on tenant.
	s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).Updates(map[string]any{
		"cas_engine_destination_name": tenant.CasEngineDestinationName,
		"pir_api_destination_name":    tenant.PirApiDestinationName,
	})

	// REGISTER_TMS_NODE is handled by CentralTmsRegistrar (Phase 3).
	// For now, mark the job finished without TMS node registration.
	// TODO(phase-3): delegate to s.RegisterTmsNode(ctx, tenant.ID)
	setStep(StepRegisterTmsNode)

	finish()
}

// ── bootstrapApplier ─────────────────────────────────────────────────────────

// bootstrapApplier performs the mutation phase of a bootstrap job.
// It is constructed after the read-only InspectTenant phase completes.
type bootstrapApplier struct {
	cfClient  *cf.CFClient
	tenant    *db.CpiTenant
	orgGUID   string
	spaceGUID string
	result    *InspectionResult
}

type credentialAction struct {
	DestinationName string `json:"destinationName"`
	ActionType      string `json:"actionType"` // "created" | "updated" | "skipped"
}

func newBootstrapApplier(tenant *db.CpiTenant, cfToken string, result *InspectionResult) (*bootstrapApplier, error) {
	cfcl, err := cf.NewCFClient(tenant.CfApiURL(), cfToken)
	if err != nil {
		return nil, fmt.Errorf("bootstrapApplier: CF client: %w", err)
	}
	return &bootstrapApplier{
		cfClient:  cfcl,
		tenant:    tenant,
		orgGUID:   result.OrgGUID,
		spaceGUID: tenant.CfSpace,
		result:    result,
	}, nil
}

// ensureServiceInstance creates a managed service instance if one does not yet
// exist.  Returns the instance GUID (existing or newly created).
//
// Lookup is by instanceName (not by plan) so that cpi-delivery's dedicated
// instance is found unambiguously, even if other instances of the same plan
// exist in the subscriber's space.
func (b *bootstrapApplier) ensureServiceInstance(ctx context.Context, offering, plan, instanceName string) (string, error) {
	// Look up the cpi-delivery–owned instance by its fixed name.
	existing, err := b.cfClient.GetServiceInstanceByName(ctx, b.spaceGUID, instanceName)
	if err != nil {
		return "", fmt.Errorf("check instance (name=%s): %w", instanceName, err)
	}
	if existing != nil {
		return existing.GUID, nil
	}

	planGUID, err := b.cfClient.GetServicePlanGUID(ctx, b.orgGUID, offering, plan)
	if err != nil {
		return "", fmt.Errorf("get plan GUID (offering=%s, plan=%s): %w", offering, plan, err)
	}

	guid, err := b.cfClient.CreateManagedServiceInstance(ctx, b.spaceGUID, planGUID, instanceName)
	if err != nil {
		return "", fmt.Errorf("create instance %q: %w", instanceName, err)
	}
	return guid, nil
}

// ensureInstanceAndKey creates a service instance (if missing) and a service
// key (if missing) for the given offering/plan.  Updates result.ServiceKeyGUIDs.
// Returns the credential actions taken.
func (b *bootstrapApplier) ensureInstanceAndKey(
	ctx context.Context,
	offering, plan string,
	instanceName, keyName string,
	missingCode string,
	instanceGUIDPtr *string,
	result *InspectionResult,
) ([]credentialAction, error) {
	// Ensure instance exists.
	if *instanceGUIDPtr == "" {
		guid, err := b.ensureServiceInstance(ctx, offering, plan, instanceName)
		if err != nil {
			return nil, err
		}
		*instanceGUIDPtr = guid
	}
	instanceGUID := *instanceGUIDPtr

	// Ensure service key exists.
	if _, hasKey := result.ServiceKeyGUIDs[instanceGUID]; hasKey {
		return nil, nil // already present; nothing to do
	}

	keyGUID, err := b.cfClient.CreateServiceKey(ctx, instanceGUID, keyName)
	if err != nil {
		return nil, fmt.Errorf("create service key (%s): %w", missingCode, err)
	}
	result.ServiceKeyGUIDs[instanceGUID] = keyGUID

	return []credentialAction{{DestinationName: keyName, ActionType: "created"}}, nil
}

// ensureDestinations manages all destination writes for one bootstrap run.
//
// ── Subscriber-side (this function) ──────────────────────────────────────────
// Creates or updates destinations inside the subscriber's own Destination
// Service instance (result.DestinationServiceInstanceGUID).  A temporary
// service key is created on that instance, used for the CF Destination Service
// REST API calls, then deleted via defer.
//
// Destinations written into the subscriber's Destination Service:
//
//   - CloudIntegration       (PIR api credentials)
//     URL: <pirRoot>/api/1.0/transportmodule/Transport
//     Read by CAS to invoke the CPI Transport Module API.
//
//   - ContentAssemblyService (CAS standard credentials)
//     Read by CAS to invoke the content-agent-assembly worker.
//
//   - TransportManagementService
//     Deferred to Phase 3 — URL comes from CentralTmsContext after TMS node
//     registration.
//
// ── Provider-side (NOT created here) ─────────────────────────────────────────
// Two per-tenant destinations in cpi-delivery's own provider-side environment:
//
//   - CPIDELIVERY_CAS_{id}   (CAS application credentials)
//     Used by TrResolver at transport time to call the subscriber's CAS export API.
//
//   - CPIDELIVERY_PIR_{id}   (PIR api credentials)
//     Used by TrResolver to call the subscriber's PIR runtime.
//
// These names are recorded on the tenant struct by buildRequiredDestinations,
// but the actual Destination Service entries in the provider environment are
// created in Phase 5 (SaaS migration), not here.
func (b *bootstrapApplier) ensureDestinations(ctx context.Context, tenant *db.CpiTenant, result *InspectionResult) ([]credentialAction, error) {
	if result.DestinationServiceInstanceGUID == "" {
		return nil, fmt.Errorf("destination service instance GUID is empty")
	}

	// ── Subscriber-side: create a temporary key on the subscriber's Destination
	// Service instance to authenticate against the Destination Service REST API.
	// The key is deleted immediately after this function returns.
	tempKeyName := fmt.Sprintf("cpidelivery-bootstrap-%d", time.Now().Unix())
	keyGUID, err := b.cfClient.CreateServiceKey(ctx, result.DestinationServiceInstanceGUID, tempKeyName)
	if err != nil {
		return nil, fmt.Errorf("create temp service key for destination bootstrap: %w", err)
	}
	defer func() { _ = b.cfClient.DeleteServiceKey(ctx, keyGUID) }()

	creds, err := b.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
	if err != nil {
		return nil, fmt.Errorf("get temp service key credentials: %w", err)
	}

	destClient, err := cf.NewDestinationServiceClient(ctx, creds)
	if err != nil {
		return nil, fmt.Errorf("build destination service client: %w", err)
	}

	var actions []credentialAction

	// Build the subscriber-side destination payloads from service key credentials.
	// Provider-side destination names (CPIDELIVERY_*) are also recorded on the
	// tenant struct inside this call, but no provider-side writes occur here.
	destinations, err := b.buildRequiredDestinations(ctx, result, tenant)
	if err != nil {
		return nil, err
	}

	// Upsert each subscriber-side destination into the subscriber's Destination Service.
	for _, dest := range destinations {
		existing, err := destClient.GetDestination(ctx, dest.Name)
		if err != nil {
			return nil, fmt.Errorf("check destination %q: %w", dest.Name, err)
		}
		actionType := "created"
		if existing != nil {
			actionType = "updated"
		}
		if err := destClient.UpsertDestination(ctx, dest); err != nil {
			return nil, fmt.Errorf("upsert destination %q: %w", dest.Name, err)
		}
		actions = append(actions, credentialAction{DestinationName: dest.Name, ActionType: actionType})
	}

	return actions, nil
}

// buildRequiredDestinations constructs the cf.Destination payloads for the
// subscriber's Destination Service.
//
// ── Subscriber-side destinations (returned, written by ensureDestinations) ───
//
//   - CloudIntegration       ← PIR api service key credentials
//     URL: <pirRoot>/api/1.0/transportmodule/Transport
//     Read by CAS to invoke the CPI Transport Module API.
//
//   - ContentAssemblyService ← CAS standard service key credentials
//     Read by CAS to invoke the content-agent-assembly worker.
//
//   - TransportManagementService
//     Deferred to Phase 3 — not included in the returned slice yet.
//
// ── Provider-side destinations (names recorded only, NOT written here) ───────
//
// The two provider-side destinations live in cpi-delivery's own environment
// (provider Destination Service), not in the subscriber's space:
//
//   - CPIDELIVERY_CAS_{id}   ← CAS application service key (content-agent/application)
//     Used by TrResolver at transport time to call the subscriber's CAS export API.
//     Created in Phase 5 (SaaS migration).
//
//   - CPIDELIVERY_PIR_{id}   ← PIR api service key (it-rt/api)
//     Used by TrResolver to call the subscriber's PIR runtime.
//     Created in Phase 5 (SaaS migration).
//
// This function records these names on the tenant struct for persistence by
// the caller, but does not write anything to the provider environment.
func (b *bootstrapApplier) buildRequiredDestinations(ctx context.Context, result *InspectionResult, tenant *db.CpiTenant) ([]cf.Destination, error) {
	var dests []cf.Destination

	// CloudIntegration — PIR api service key credentials.
	// URL pattern: <pirRoot>/api/1.0/transportmodule/Transport  (SAP Transport Module)
	if result.PirApiInstanceGUID != "" {
		keyGUID, ok := result.ServiceKeyGUIDs[result.PirApiInstanceGUID]
		if ok {
			creds, err := b.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
			if err != nil {
				return nil, fmt.Errorf("get PIR api key credentials: %w", err)
			}
			if d, ok := buildOAuthDestination("CloudIntegration", transportModuleURL, tenant.ID, creds); ok {
				dests = append(dests, d)
			}
		}
	}

	// ContentAssemblyService — CAS standard service key credentials.
	// The destination name "ContentAssemblyService" is fixed by SAP convention;
	// CAS reads it to reach the content-agent-assembly worker.
	if result.CasStandardInstanceGUID != "" {
		keyGUID, ok := result.ServiceKeyGUIDs[result.CasStandardInstanceGUID]
		if ok {
			creds, err := b.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
			if err != nil {
				return nil, fmt.Errorf("get CAS standard key credentials: %w", err)
			}
			if d, ok := buildOAuthDestination("ContentAssemblyService", nil, tenant.ID, creds); ok {
				dests = append(dests, d)
			}
		}
	}

	// TransportManagementService — deferred to Phase 3.
	// URL is derived from CentralTmsContext.TmsApiEndpoint after TMS node
	// registration completes.  Bootstrap will fill this in during REGISTER_TMS_NODE.

	// Record provider-side destination names for persistence.
	// Actual creation of these destinations is Phase 5 (SaaS migration).
	tenant.CasEngineDestinationName = fmt.Sprintf("CPIDELIVERY_CAS_%d", tenant.ID)
	tenant.PirApiDestinationName = fmt.Sprintf("CPIDELIVERY_PIR_%d", tenant.ID)

	return dests, nil
}

// transportModuleURL appends the fixed CPI Transport Module path to a PIR api
// root URL.  CAS reads the CloudIntegration destination and calls this endpoint
// to hand off the assembled MTAR for transport.
func transportModuleURL(rootURL string) string {
	// Trim any trailing slash before appending the fixed path.
	for len(rootURL) > 0 && rootURL[len(rootURL)-1] == '/' {
		rootURL = rootURL[:len(rootURL)-1]
	}
	return rootURL + "/api/1.0/transportmodule/Transport"
}

// buildOAuthDestination constructs a cf.Destination for OAuth2ClientCredentials
// authentication from a service key credentials map.
//
// urlTransform is an optional function applied to the "url" field from the
// credentials before it is written to the destination.  Pass nil to use the
// credential URL as-is.  For example, CloudIntegration requires appending
// the Transport Module path: transportModuleURL.
//
// Returns false if any required credential field (url, tokenurl, clientid,
// clientsecret) is absent.
func buildOAuthDestination(name string, urlTransform func(string) string, tenantID uint, creds map[string]any) (cf.Destination, bool) {
	rawURL, _ := creds["url"].(string)
	tokenURL, _ := creds["tokenurl"].(string)
	clientID, _ := creds["clientid"].(string)
	clientSecret, _ := creds["clientsecret"].(string)
	if rawURL == "" || tokenURL == "" || clientID == "" || clientSecret == "" {
		return cf.Destination{}, false
	}

	destURL := rawURL
	if urlTransform != nil {
		destURL = urlTransform(rawURL)
	}

	return cf.Destination{
		Name:           name,
		Description:    fmt.Sprintf("DO NOT MODIFY. Created by cpi-delivery bootstrap for tenant %d", tenantID),
		Type:           "HTTP",
		URL:            destURL,
		Authentication: "OAuth2ClientCredentials",
		ProxyType:      "Internet",
		AdditionalProperties: map[string]string{
			"tokenServiceURL":     tokenURL,
			"clientId":            clientID,
			"clientSecret":        clientSecret,
			"tokenServiceURLType": "Dedicated",
		},
	}, true
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *Service) getTenantForBootstrap(tenantID uint) (*db.CpiTenant, error) {
	var tenant db.CpiTenant
	if err := s.DB.First(&tenant, tenantID).Error; err != nil {
		return nil, fmt.Errorf("tenant %d not found: %w", tenantID, err)
	}
	return &tenant, nil
}

func newInspector(tenant *db.CpiTenant, cfToken string) (*TenantInspector, error) {
	return NewTenantInspector(tenant.CfApiURL(), cfToken)
}
