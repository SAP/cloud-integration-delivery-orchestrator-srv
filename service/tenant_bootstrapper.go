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

	"gorm.io/gorm"
)

// ── Public service entry points ───────────────────────────────────────────────

// PreviewBootstrap runs a read-only inspection of the tenant's local
// prerequisites and returns a BootstrapPreview that describes what is present,
// what is missing, and what would be created by ApplyBootstrap.
//
// It does NOT create a TenantBootstrapJob row — preview is lightweight and
// read-only.  The result is returned directly to the caller (handler).
//
// No cfToken is required because InspectCfSubaccount is the only operation called.
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

	result, err := inspector.InspectCfSubaccount(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("preview: inspect: %w", err)
	}
	s.checkTmsSourceNode(tenant, result)
	s.checkCentralTmsContext(result)

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

	// Infer job type from the current state before Phase 1 transitions it.
	// not_ready means a previous attempt failed — this run is a retry.
	// draft and configured are first-time applies.
	// ready means the operator is intentionally re-applying (e.g. to refresh
	// service key credentials or re-sync destinations) — still labeled apply,
	// not retry, because no failure preceded it.
	jobType := lifecycle.JobTypeApply
	if tenant.LifecycleState == lifecycle.TenantNotReady {
		jobType = lifecycle.JobTypeRetry
	}

	// ── Phase 1: Atomically claim readying state ──────────────────────────────
	// TransitionLifecycle uses SELECT FOR UPDATE inside its own short transaction,
	// so the check-and-write is atomic across all instances.  EventBootstrapStarted
	// is not a valid edge from TenantReadying (allowedTransitions), so any concurrent
	// caller that already claimed readying is rejected here with ErrTransitionNotAllowed
	// — before either caller makes a single CF API call.
	if err := s.TransitionLifecycle(tenantID, EventBootstrapStarted); err != nil {
		return 0, fmt.Errorf("apply: %w", err)
	}
	// Reset all prerequisite statuses and clear stale error from any previous run.
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
		"blocking_reason":                    "",
		"pir_api_status":                     lifecycle.PrereqMissing,
		"cas_application_status":             lifecycle.PrereqMissing,
		"cas_standard_status":                lifecycle.PrereqMissing,
		"cloud_integration_dest_status":      lifecycle.PrereqMissing,
		"content_assembly_dest_status":       lifecycle.PrereqMissing,
		"transport_management_dest_status":   lifecycle.PrereqMissing,
	}).Error; err != nil {
		env.Logger().Warnw("apply: failed to reset prereq statuses", "tenantID", tenantID, "error", err)
	}

	// ── Phase 2: Synchronous inspect ─────────────────────────────────────────
	// State is now readying.  Any failure from here must roll back to not_ready
	// so the operator can issue another ApplyBootstrap.
	inspector, err := newInspector(tenant, cfToken)
	if err != nil {
		_ = s.TransitionLifecycle(tenantID, EventBootstrapFailed)
		return 0, fmt.Errorf("apply: build inspector: %w", err)
	}
	result, err := inspector.InspectCfSubaccount(ctx, tenant)
	if err != nil {
		_ = s.TransitionLifecycle(tenantID, EventBootstrapFailed)
		return 0, fmt.Errorf("apply: inspect: %w", err)
	}
	s.checkTmsSourceNode(tenant, result)
	s.checkCentralTmsContext(result)
	if len(result.PermissionIssues) > 0 {
		_ = s.TransitionLifecycle(tenantID, EventBootstrapFailed)
		return 0, fmt.Errorf("apply: permission issues: %v", result.PermissionIssues)
	}
	if len(result.WaitingUserAction) > 0 {
		_ = s.TransitionLifecycle(tenantID, EventBootstrapFailed)
		return 0, fmt.Errorf("apply: waiting user action: %v", result.WaitingUserAction)
	}

	// ── Phase 3: Create job row and launch goroutine ──────────────────────────
	// Lifecycle is already readying; no further transition needed here.
	// If job creation fails, roll back so the operator can retry.
	missingJSON, _ := json.Marshal(result.MissingItems)
	job := &db.TenantBootstrapJob{
		CpiTenantID:          tenantID,
		JobType:              jobType,
		State:                lifecycle.JobRunning,
		MissingPrerequisites: missingJSON,
		StartedAt:            time.Now(),
	}
	if err := s.DB.Create(job).Error; err != nil {
		_ = s.TransitionLifecycle(tenantID, EventBootstrapFailed)
		return 0, fmt.Errorf("apply: create job: %w", err)
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
// already produced by the synchronous inspect phase in ApplyBootstrap — it
// does NOT re-run InspectCfSubaccount.
//
// runBootstrap must NOT be called directly — use ApplyBootstrap.
func (s *Service) runBootstrap(tenant *db.CpiTenant, jobID uint, cfToken string, result *InspectionResult) {
	ctx := context.Background()

	fail := func(failureType lifecycle.BootstrapFailureType, step, reason string) {
		state := lifecycle.JobFailed
		if failureType == lifecycle.FailureWaitingUserAction {
			state = lifecycle.JobWaitingUserAction
		}
		now := time.Now()
		// Job state and lifecycle transition are atomic: if either fails the
		// tenant stays in readying and the operator can use ResetBootstrap.
		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).Updates(map[string]any{
				"state":        state,
				"failure_type": failureType,
				"current_step": step,
				"ended_at":     &now,
				"error_detail": reason,
			}).Error; err != nil {
				return fmt.Errorf("update job state: %w", err)
			}
			return s.transitionLifecycleWithTx(tx, tenant.ID, EventBootstrapFailed)
		}); err != nil {
			env.Logger().Errorw("bootstrap: failed to record job failure; tenant may be stuck in readying",
				"tenantID", tenant.ID, "jobID", jobID, "error", err)
		}
		// blocking_reason is display-only; persist best-effort outside the
		// transaction so its failure cannot roll back the critical state writes above.
		if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
			Update("blocking_reason", reason).Error; err != nil {
			env.Logger().Errorw("bootstrap: failed to persist blocking_reason",
				"tenantID", tenant.ID, "jobID", jobID, "error", err)
		}
	}

	finish := func() {
		now := time.Now()
		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).Updates(map[string]any{
				"state":    lifecycle.JobFinished,
				"ended_at": &now,
			}).Error; err != nil {
				return fmt.Errorf("update job state: %w", err)
			}
			return s.transitionLifecycleWithTx(tx, tenant.ID, EventBootstrapFinished)
		}); err != nil {
			env.Logger().Errorw("bootstrap: failed to record job completion; tenant may be stuck in readying",
				"tenantID", tenant.ID, "jobID", jobID, "error", err)
		}
	}

	setStep := func(step string) {
		if err := s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
			Update("current_step", step).Error; err != nil {
			env.Logger().Errorw("bootstrap: failed to update job step",
				"tenantID", tenant.ID, "jobID", jobID, "step", step, "error", err)
		}
	}

	markReady := func(field string) {
		if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
			Update(field, lifecycle.PrereqReady).Error; err != nil {
			env.Logger().Errorw("bootstrap: failed to update prereq status",
				"tenantID", tenant.ID, "field", field, "error", err)
		}
	}

	// ── Apply missing items ───────────────────────────────────────────────────

	bootstrapper, err := newBootstrapApplier(tenant, cfToken, result, s.ProviderDest)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, "", fmt.Sprintf("build applier: %s", err))
		return
	}

	var credentialActions []credentialAction

	// CHECK_PIR_API → create instance + service key if missing
	setStep(StepCheckPirApi)
	acts, err := bootstrapper.ensureInstanceAndKey(ctx, offeringPIR, planPirApi,
		instanceNamePirApi, keyNamePirApi,
		missingCodePirApi, &result.PirApiInstanceGUID, result,
		map[string]any{
			"grant-types":   []string{"client_credentials", "password"},
			"redirect-uris": []string{},
			"roles":         []string{"AuthGroup_IntegrationDeveloper", "WorkspacePackagesTransport"},
		})
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckPirApi, err.Error())
		return
	}
	credentialActions = append(credentialActions, acts...)
	if result.PirApiInstanceGUID != "" {
		markReady("pir_api_status")
	}

	// CHECK_CAS_APPLICATION → create instance + service key if missing.
	// Note: the CAS application service key is NOT used to build a subscriber-side
	// destination here.  It will be consumed by TrResolver (Phase 4) via the
	// provider-side CPIDELIVERY_CAS_{id} destination.
	setStep(StepCheckCasApplication)
	acts, err = bootstrapper.ensureInstanceAndKey(ctx, offeringCAS, planCasApplication,
		instanceNameCasApplication, keyNameCasApplication,
		missingCodeCasApplication, &result.CasApplicationInstanceGUID, result,
		map[string]any{
			"roles": []string{"Security Operator", "Admin", "Read", "Import", "Export"},
		})
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckCasApplication, err.Error())
		return
	}
	credentialActions = append(credentialActions, acts...)
	if result.CasApplicationInstanceGUID != "" {
		markReady("cas_application_status")
	}

	// CHECK_CAS_STANDARD → create instance + service key if missing
	setStep(StepCheckCasStandard)
	acts, err = bootstrapper.ensureInstanceAndKey(ctx, offeringCAS, planCasStandard,
		instanceNameCasStandard, keyNameCasStandard,
		missingCodeCasStandard, &result.CasStandardInstanceGUID, result,
		map[string]any{
			"roles": []string{"Assemble"},
		})
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckCasStandard, err.Error())
		return
	}
	credentialActions = append(credentialActions, acts...)
	if result.CasStandardInstanceGUID != "" {
		markReady("cas_standard_status")
	}

	// CHECK_DESTINATION_SERVICE → create instance if missing.
	// Grouped with CHECK_DESTINATIONS: the instance is the vehicle for writing
	// the destination configs that depend on the PIR/CAS credentials above.
	//
	// Unlike PIR/CAS, the Destination Service does NOT need a persistent service
	// key managed here.  ensureSubscriberDestinations creates a short-lived temporary key
	// (deleted via defer) solely to call the Destination Service REST API, so
	// ensureInstanceAndKey is not used for this service.
	setStep(StepCheckDestinationService)
	if result.DestinationServiceInstanceGUID == "" {
		guid, err := bootstrapper.ensureServiceInstance(ctx, offeringDestination, planDestinationLite,
			instanceNameDestinationLite, nil)
		if err != nil {
			fail(lifecycle.FailureRemoteSystemError, StepCheckDestinationService, err.Error())
			return
		}
		result.DestinationServiceInstanceGUID = guid
	}

	// CHECK_DESTINATIONS → write subscriber-side destinations into the subscriber's Destination Service instance
	setStep(StepCheckDestinations)
	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		fail(lifecycle.FailureWaitingUserAction, StepCheckCentralTmsContext,
			"CENTRAL_TMS_NOT_CONFIGURED: "+err.Error())
		return
	}
	destActs, err := bootstrapper.ensureSubscriberDestinations(ctx, tenant, result, tmsCtx)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckDestinations, err.Error())
		return
	}
	credentialActions = append(credentialActions, destActs...)
	// CloudIntegration and ContentAssemblyService are both written by ensureSubscriberDestinations.
	// Mark them ready only if the underlying service instance (and thus key) was present.
	if result.PirApiInstanceGUID != "" {
		markReady("cloud_integration_dest_status")
	}
	if result.CasStandardInstanceGUID != "" {
		markReady("content_assembly_dest_status")
	}
	if tmsCtx != nil && s.ProviderDest != nil {
		markReady("transport_management_dest_status")
	}

	// Write provider-side CPIDELIVERY_PIR_{id} and CPIDELIVERY_CAS_{id} into
	// cpi-delivery's own Destination Service.  Both destinations are runtime-critical:
	// TrResolver depends on them for deploy and TR generation.
	// ProviderDest is guaranteed non-nil at startup (main.go panics on init failure);
	// a nil here indicates a programming error and must surface immediately.
	if s.ProviderDest == nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckDestinations,
			"ProviderDest is nil — provider Destination Service client was not injected at startup")
		return
	}
	provActs, err := bootstrapper.ensureProviderDestinations(ctx, tenant, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckDestinations, err.Error())
		return
	}
	credentialActions = append(credentialActions, provActs...)

	// Persist credential action log (no secrets — names and action types only).
	// Audit-only; failure is best-effort.
	actsJSON, _ := json.Marshal(credentialActions)
	if err := s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
		Update("credential_actions", actsJSON).Error; err != nil {
		env.Logger().Errorw("bootstrap: failed to persist credential_actions",
			"tenantID", tenant.ID, "jobID", jobID, "error", err)
	}

	// Persist provider-side destination names on tenant.
	// Runtime-critical: TrResolver uses these names on every deploy / TR generation.
	// A failure here must abort bootstrap — marking the tenant ready without these
	// names would cause all runtime operations to fail silently after a restart.
	persistUpdates := map[string]any{
		"cas_engine_destination_name": tenant.CasEngineDestinationName,
		"pir_api_destination_name":    tenant.PirApiDestinationName,
		"pir_api_url":                 tenant.PirApiUrl,
	}
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).Updates(persistUpdates).Error; err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckDestinations,
			fmt.Sprintf("persist destination names: %s", err))
		return
	}

	// REGISTER_TMS_NODE is handled by the independent TMS Node registration
	// lifecycle (Phase 3 — service/tms_node_registrar.go).  Bootstrap finishes
	// here; the operator triggers TMS registration separately via
	// POST /tms-node/register after bootstrap completes.
	setStep(StepRegisterTmsNode)

	finish()
}

// ── bootstrapApplier ─────────────────────────────────────────────────────────

// bootstrapApplier performs the mutation phase of a bootstrap job.
// It is constructed after the read-only InspectCfSubaccount phase completes.
type bootstrapApplier struct {
	cfClient     *cf.CFClient
	tenant       *db.CpiTenant
	orgGUID      string
	spaceGUID    string
	result       *InspectionResult
	providerDest *cf.DestinationServiceClient // provider-side Destination Service for CPIDELIVERY_* destinations
}

type credentialAction struct {
	DestinationName string `json:"destinationName"`
	ActionType      string `json:"actionType"` // "created" | "updated" | "skipped"
}

func newBootstrapApplier(tenant *db.CpiTenant, cfToken string, result *InspectionResult, providerDest *cf.DestinationServiceClient) (*bootstrapApplier, error) {
	cfcl, err := cf.NewCFClient(tenant.CfApiURL(), cfToken)
	if err != nil {
		return nil, fmt.Errorf("bootstrapApplier: CF client: %w", err)
	}
	return &bootstrapApplier{
		cfClient:     cfcl,
		tenant:       tenant,
		orgGUID:      result.OrgGUID,
		spaceGUID:    tenant.CfSpace,
		result:       result,
		providerDest: providerDest,
	}, nil
}

// ensureServiceInstance creates a managed service instance if one does not yet
// exist.  Returns the instance GUID (existing or newly created).
//
// Lookup is by instanceName (not by plan) so that cpi-delivery's dedicated
// instance is found unambiguously, even if other instances of the same plan
// exist in the subscriber's space.
//
// parameters is forwarded verbatim to the CF service broker (equivalent to
// `cf create-service … -c '{…}'`).  Pass nil when no parameters are required.
func (b *bootstrapApplier) ensureServiceInstance(ctx context.Context, offering, plan, instanceName string, parameters map[string]any) (string, error) {
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

	guid, err := b.cfClient.CreateManagedServiceInstance(ctx, b.spaceGUID, planGUID, instanceName, parameters)
	if err != nil {
		return "", fmt.Errorf("create instance %q: %w", instanceName, err)
	}
	return guid, nil
}

// ensureInstanceAndKey creates a service instance (if missing) and a service
// key (if missing) for the given offering/plan.  Updates result.ServiceKeyGUIDs.
// Returns the credential actions taken.
//
// parameters is forwarded to the CF service broker on instance creation.
// Pass nil when no broker parameters are required.
func (b *bootstrapApplier) ensureInstanceAndKey(
	ctx context.Context,
	offering, plan string,
	instanceName, keyName string,
	missingCode string,
	instanceGUIDPtr *string,
	result *InspectionResult,
	parameters map[string]any,
) ([]credentialAction, error) {
	// Ensure instance exists.
	if *instanceGUIDPtr == "" {
		guid, err := b.ensureServiceInstance(ctx, offering, plan, instanceName, parameters)
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

// ensureSubscriberDestinations writes all subscriber-side destinations for one
// bootstrap run into the subscriber's own Destination Service instance
// (result.DestinationServiceInstanceGUID).
//
// A temporary service key is created on that instance to authenticate against
// the Destination Service REST API, then deleted via defer.
//
// Destinations written into the subscriber's Destination Service:
//
//   - CloudIntegration       (PIR api credentials)
//   - ContentAssemblyService (CAS standard credentials)
//   - TransportManagementService (TMS credentials copied from provider-side TmsApiDestinationName)
//
// Provider-side destinations (CPIDELIVERY_PIR_{id}, CPIDELIVERY_CAS_{id}) are
// NOT written here — see ensureProviderDestinations.
func (b *bootstrapApplier) ensureSubscriberDestinations(ctx context.Context, tenant *db.CpiTenant, result *InspectionResult, tmsCtx *db.CentralTmsContext) ([]credentialAction, error) {
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

	// Build subscriber-side destination payloads from service key credentials.
	destinations, err := b.buildSubscriberDestinations(ctx, result, tenant, tmsCtx)
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

// buildSubscriberDestinations constructs the cf.Destination payloads to be
// written into the subscriber's own Destination Service.
//
//   - CloudIntegration       ← PIR api service key credentials
//   - ContentAssemblyService ← CAS standard service key credentials
//   - TransportManagementService ← TMS OAuth credentials copied from provider-side TmsApiDestinationName
func (b *bootstrapApplier) buildSubscriberDestinations(ctx context.Context, result *InspectionResult, tenant *db.CpiTenant, tmsCtx *db.CentralTmsContext) ([]cf.Destination, error) {
	var dests []cf.Destination

	// CloudIntegration — PIR api service key credentials.
	// URL pattern: <pirRoot>/api/1.0/transportmodule/Transport  (SAP Transport Module)
	if result.PirApiInstanceGUID != "" {
		keyGUID, ok := result.ServiceKeyGUIDs[result.PirApiInstanceGUID]
		if !ok {
			return nil, fmt.Errorf("no service key GUID for PIR api instance %q", result.PirApiInstanceGUID)
		}
		creds, err := b.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
		if err != nil {
			return nil, fmt.Errorf("get PIR api key credentials: %w", err)
		}
		d, err := buildOAuthDestination("CloudIntegration", transportModuleURL, tenant.ID, creds)
		if err != nil {
			return nil, err
		}
		dests = append(dests, d)
	}

	// ContentAssemblyService — CAS standard service key credentials.
	// The destination name "ContentAssemblyService" is fixed by SAP convention;
	// CAS reads it to reach the content-agent-assembly worker.
	if result.CasStandardInstanceGUID != "" {
		keyGUID, ok := result.ServiceKeyGUIDs[result.CasStandardInstanceGUID]
		if !ok {
			return nil, fmt.Errorf("no service key GUID for CAS standard instance %q", result.CasStandardInstanceGUID)
		}
		creds, err := b.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
		if err != nil {
			return nil, fmt.Errorf("get CAS standard key credentials: %w", err)
		}
		d, err := buildOAuthDestination("ContentAssemblyService", nil, tenant.ID, creds)
		if err != nil {
			return nil, err
		}
		dests = append(dests, d)
	}

	// TransportManagementService — TMS OAuth credentials copied from the provider-side
	// TmsApiDestinationName destination.  The URL is the fixed TMS backend endpoint.
	// Both destinations share the same TMS subscription credentials (RFC 013 §10).
	// sourceSystemId must match the tenant's TMS source node name (RFC 013 §17).
	if tmsCtx != nil && tmsCtx.TmsApiDestinationName != "" && b.providerDest != nil {
		tmsDest, err := b.providerDest.GetDestination(ctx, tmsCtx.TmsApiDestinationName)
		if err != nil {
			return nil, fmt.Errorf("get TMS API destination %q from provider: %w", tmsCtx.TmsApiDestinationName, err)
		}
		if tmsDest == nil {
			return nil, fmt.Errorf("TMS API destination %q not found in provider Destination Service", tmsCtx.TmsApiDestinationName)
		}
		dest := *tmsDest
		dest.Name = "TransportManagementService"
		dest.Description = fmt.Sprintf("DO NOT MODIFY. Created by cpi-delivery bootstrap for tenant %d", tenant.ID)
		if tenant.TmsSourceNodeName == "" {
			return nil, fmt.Errorf("TmsSourceNodeName is empty — Apply should have been blocked by checkTmsSourceNode")
		}
		dest.SourceSystemId = tenant.TmsSourceNodeName
		dests = append(dests, dest)
	}

	return dests, nil
}

// ensureProviderDestinations writes the two per-tenant destinations into
// cpi-delivery's own provider-side Destination Service.
//
// These are runtime-critical: without them TrResolver cannot reach the
// subscriber's PIR runtime or CAS engine.
//
//   - CPIDELIVERY_PIR_{id}  — PIR api base URL + credentials (it-rt/api)
//     Used by TrResolver to list artifacts and call deploy endpoints.
//     URL is the root PIR URL, NOT the Transport Module path
//     (/api/1.0/transportmodule/Transport is the subscriber-side CloudIntegration
//     destination URL; these two destinations serve different callers).
//
//   - CPIDELIVERY_CAS_{id}  — CAS application base URL + credentials (content-agent/application)
//     Used by TrResolver to call the subscriber's CAS export API to generate TRs.
//
// This function names, creates, and records the provider-side destinations.
// Unlike subscriber-side destinations (which use a temporary CF service key),
// these are written directly via cf.DestinationServiceClient.UpsertDestination.
func (b *bootstrapApplier) ensureProviderDestinations(ctx context.Context, tenant *db.CpiTenant, result *InspectionResult) ([]credentialAction, error) {
	if b.providerDest == nil {
		return nil, fmt.Errorf("ensureProviderDestinations: ProviderDest is nil — provider Destination Service client was not injected at startup")
	}

	// Set provider-side destination names on the tenant struct for persistence.
	// Naming is owned here — no other function should set these fields.
	tenant.PirApiDestinationName = fmt.Sprintf("CPIDELIVERY_PIR_%d", tenant.ID)
	tenant.CasEngineDestinationName = fmt.Sprintf("CPIDELIVERY_CAS_%d", tenant.ID)

	var actions []credentialAction

	// CPIDELIVERY_PIR_{id} — PIR api root URL (base URL, not transport module path).
	if result.PirApiInstanceGUID != "" {
		keyGUID, ok := result.ServiceKeyGUIDs[result.PirApiInstanceGUID]
		if ok {
			creds, err := b.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
			if err != nil {
				return nil, fmt.Errorf("get PIR api key credentials for provider dest: %w", err)
			}
			dest, err := buildOAuthDestination(tenant.PirApiDestinationName, nil /* base URL as-is */, tenant.ID, creds)
			if err != nil {
				return nil, err
			}
			if err := b.providerDest.UpsertDestination(ctx, dest); err != nil {
				return nil, fmt.Errorf("upsert provider dest %q: %w", dest.Name, err)
			}
			// Store PIR root URL for display in the tenant info tab.
			if pirURL, _ := creds["url"].(string); pirURL != "" {
				tenant.PirApiUrl = pirURL
			}
			actions = append(actions, credentialAction{DestinationName: dest.Name, ActionType: "upserted"})
		}
	}

	// CPIDELIVERY_CAS_{id} — CAS application root URL (content-agent/application).
	if result.CasApplicationInstanceGUID != "" {
		keyGUID, ok := result.ServiceKeyGUIDs[result.CasApplicationInstanceGUID]
		if ok {
			creds, err := b.cfClient.GetServiceKeyCredentials(ctx, keyGUID)
			if err != nil {
				return nil, fmt.Errorf("get CAS application key credentials for provider dest: %w", err)
			}
			dest, err := buildOAuthDestination(tenant.CasEngineDestinationName, nil /* base URL as-is */, tenant.ID, creds)
			if err != nil {
				return nil, err
			}
			if err := b.providerDest.UpsertDestination(ctx, dest); err != nil {
				return nil, fmt.Errorf("upsert provider dest %q: %w", dest.Name, err)
			}
			actions = append(actions, credentialAction{DestinationName: dest.Name, ActionType: "upserted"})
		}
	}

	return actions, nil
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
// SAP BTP service key credential structures vary by service:
//   - it-rt/api nests everything under "oauth":
//     {"oauth": {"url": "<service>", "tokenurl": "<token>", "clientid": "...", "clientsecret": "..."}}
//   - content-agent uses top-level "url" + "uaa" nested OAuth fields:
//     {"url": "<service>", "uaa": {"clientid": "...", "clientsecret": "...", "url": "<token-base>"}}
//
// Resolution order: flat top-level → "uaa" nested → "oauth" nested.
//
// urlTransform is an optional function applied to the resolved service URL before it
// is written to the destination.  Pass nil to use the credential URL as-is.
//
// Returns an error if required credential fields cannot be resolved.
func buildOAuthDestination(name string, urlTransform func(string) string, tenantID uint, creds map[string]any) (cf.Destination, error) {
	// Service base URL: top-level "url" (preferred) or "uri".
	rawURL, _ := creds["url"].(string)
	if rawURL == "" {
		rawURL, _ = creds["uri"].(string)
	}

	// OAuth fields: try flat top-level first, then fall back to nested "uaa".
	tokenURL, _ := creds["tokenurl"].(string)
	clientID, _ := creds["clientid"].(string)
	clientSecret, _ := creds["clientsecret"].(string)

	if tokenURL == "" || clientID == "" || clientSecret == "" {
		if uaa, ok := creds["uaa"].(map[string]any); ok {
			if tokenURL == "" {
				tokenURL, _ = uaa["tokenurl"].(string)
				if tokenURL == "" {
					// content-agent puts the UAA base URL in uaa.url (no tokenurl field).
					tokenURL, _ = uaa["url"].(string)
				}
			}
			if clientID == "" {
				clientID, _ = uaa["clientid"].(string)
			}
			if clientSecret == "" {
				clientSecret, _ = uaa["clientsecret"].(string)
			}
		}
	}

	// it-rt/api nests url + tokenurl + clientid + clientsecret under "oauth".
	if rawURL == "" || tokenURL == "" || clientID == "" || clientSecret == "" {
		if oauth, ok := creds["oauth"].(map[string]any); ok {
			if rawURL == "" {
				rawURL, _ = oauth["url"].(string)
			}
			if tokenURL == "" {
				tokenURL, _ = oauth["tokenurl"].(string)
			}
			if clientID == "" {
				clientID, _ = oauth["clientid"].(string)
			}
			if clientSecret == "" {
				clientSecret, _ = oauth["clientsecret"].(string)
			}
		}
	}

	if rawURL == "" || tokenURL == "" || clientID == "" || clientSecret == "" {
		return cf.Destination{}, fmt.Errorf(
			"build destination %q: incomplete service key credentials (url=%v tokenurl=%v clientid=%v clientsecret=%v)",
			name, rawURL != "", tokenURL != "", clientID != "", clientSecret != "",
		)
	}

	// Service key UAA credentials often supply only the base host without the
	// /oauth/token path (e.g. content-agent uaa.url).  Normalise before writing
	// so that the BTP Destination Service entry is always a valid token endpoint.
	tokenURL = cf.NormaliseTokenURL(tokenURL)

	destURL := rawURL
	if urlTransform != nil {
		destURL = urlTransform(rawURL)
	}

	return cf.Destination{
		Name:                name,
		Description:         fmt.Sprintf("DO NOT MODIFY. Created by cpi-delivery bootstrap for tenant %d", tenantID),
		Type:                "HTTP",
		URL:                 destURL,
		Authentication:      "OAuth2ClientCredentials",
		ProxyType:           "Internet",
		TokenServiceURL:     tokenURL,
		TokenServiceURLType: "Dedicated",
		ClientId:            clientID,
		ClientSecret:        clientSecret,
	}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// checkCentralTmsContext checks whether the CentralTmsContext is configured in the
// DB (TmsApiDestinationName non-empty).  If not, it appends "CENTRAL_TMS_NOT_CONFIGURED"
// to result.WaitingUserAction, which causes ApplyBootstrap and PreviewBootstrap to
// surface a clear blocking signal to the operator before any CF API calls are made.
func (s *Service) checkCentralTmsContext(result *InspectionResult) {
	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil || tmsCtx.TmsApiDestinationName == "" {
		result.WaitingUserAction = append(result.WaitingUserAction, "CENTRAL_TMS_NOT_CONFIGURED")
	}
}

// checkTmsSourceNode verifies that TMS Node registration is complete before
// bootstrap apply is allowed.  Without TmsSourceNodeName the bootstrap cannot
// write the sourceSystemId field on the TransportManagementService destination.
//
// This is a DB-only check (no CF API call) — analogous to checkCentralTmsContext.
// The operator must complete TMS Node registration (wizard Step 2) before Apply.
func (s *Service) checkTmsSourceNode(tenant *db.CpiTenant, result *InspectionResult) {
	if tenant.TmsSourceNodeName == "" || tenant.TmsNodeRegistrationStatus != lifecycle.PrereqReady {
		result.WaitingUserAction = append(result.WaitingUserAction, "TMS_NODE_NOT_REGISTERED")
	}
}

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
