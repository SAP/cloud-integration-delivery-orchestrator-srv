package service

// tms_extension_bootstrapper.go — TMS extension bootstrap (RFC 032 P0).
//
// This file contains the TMS extension Apply/Preview/Reset entry points and the
// async goroutine that creates CAS instances, subscriber destinations, and the
// provider-side CPIDELIVERY_CAS_{id} destination.
//
// Design: mirrors tenant_bootstrapper.go (BASE layer) but for the TMS layer.
// The two goroutines never run concurrently for the same tenant (guarded by
// atomic TmsExtensionState + LifecycleState checks).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cf"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PreviewTmsExtension runs a read-only inspection of TMS extension prerequisites.
func (s *Service) PreviewTmsExtension(ctx context.Context, tenantID uint, cfToken string) (*BootstrapPreview, error) {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return nil, err
	}
	inspector, err := newInspector(tenant, cfToken)
	if err != nil {
		return nil, fmt.Errorf("tms preview: %w", err)
	}
	result, err := s.inspectTmsExtension(ctx, inspector, tenant)
	if err != nil {
		return nil, fmt.Errorf("tms preview: inspect: %w", err)
	}
	return buildPreview(tenant, result), nil
}

// ApplyTmsExtension synchronously inspects the TMS extension prerequisites, then
// creates a Layer=TMS job and launches the goroutine.
func (s *Service) ApplyTmsExtension(ctx context.Context, tenantID uint, cfToken string) (uint, error) {
	tenant, err := s.getTenantForBootstrap(tenantID)
	if err != nil {
		return 0, err
	}

	// Phase 1 only claims mutual exclusion — prereq reset is deferred to Phase 3 (§2.1.1).
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var t db.CpiTenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "lifecycle_state", "tms_extension_state").
			First(&t, tenantID).Error; err != nil {
			return err
		}
		// Precondition: tenant must be onboarded.
		// Covers: draft, configured (not yet onboarded),
		// and readying (BASE bootstrap running — mutual exclusion).
		if !t.IsOnboarded() {
			return fmt.Errorf("tenant %d is not onboarded (LifecycleState=%s)", tenantID, t.LifecycleState)
		}
		return s.transitionTmsWithTx(tx, tenantID, TmsEventApplyStarted)
	}); err != nil {
		return 0, fmt.Errorf("tms apply: %w", err)
	}

	// Synchronous inspect (TMS extension resources).
	inspector, err := newInspector(tenant, cfToken)
	if err != nil {
		s.failTmsPhase2(tenantID, "INSPECT_FAILED")
		return 0, fmt.Errorf("tms apply: build inspector: %w", err)
	}
	result, err := s.inspectTmsExtension(ctx, inspector, tenant)
	if err != nil {
		s.failTmsPhase2(tenantID, "INSPECT_FAILED")
		return 0, fmt.Errorf("tms apply: inspect: %w", err)
	}
	if len(result.PermissionIssues) > 0 {
		s.failTmsPhase2(tenantID, "PERMISSION_BLOCKED")
		return 0, fmt.Errorf("tms apply: permission issues: %v", result.PermissionIssues)
	}
	if len(result.WaitingUserAction) > 0 {
		s.failTmsPhase2(tenantID, "WAITING_USER_ACTION")
		return 0, fmt.Errorf("tms apply: waiting user action: %v", result.WaitingUserAction)
	}

	// Phase 3: Reset prereqs, create job, launch goroutine (§2.1.1 推论 4).
	missingJSON, _ := json.Marshal(result.MissingItems)
	job := &db.TenantBootstrapJob{
		CpiTenantID:          tenantID,
		Layer:                lifecycle.LayerTMS,
		JobType:              lifecycle.JobTypeApply,
		State:                lifecycle.JobRunning,
		MissingPrerequisites: missingJSON,
		StartedAt:            time.Now(),
	}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := resetTmsPrereqs(tx, tenantID, ""); err != nil {
			return err
		}
		return tx.Create(job).Error
	}); err != nil {
		_ = s.TransitionTms(tenantID, TmsEventApplyFailed)
		return 0, fmt.Errorf("tms apply: create job: %w", err)
	}

	go s.runTmsExtensionBootstrap(tenant, job.ID, cfToken, result)
	return job.ID, nil
}

// ResetTmsExtension rolls the tenant's TMS extension back to not_started,
// clearing all TMS product fields (node binding, CAS statuses, destination statuses).
//
// Allowed from: all states except bootstrapping (no TmsEventReset edge).
// The state machine handles idempotency for not_started → not_started.
//
// All writes are atomic in a single transaction. The state transition runs
// first (SELECT FOR UPDATE), so bootstrapping is rejected before any
// field cleanup or job marking occurs.
func (s *Service) ResetTmsExtension(tenantID uint) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		// State transition first: validates state + acquires row lock.
		// bootstrapping is rejected here → rollback, zero side effects.
		if err := s.transitionTmsWithTx(tx, tenantID, TmsEventReset); err != nil {
			return fmt.Errorf("tms reset: %w", err)
		}

		// Clear all TMS prerequisite and product fields.
		if err := resetTmsPrereqs(tx, tenantID, "OPERATOR_RESET"); err != nil {
			return fmt.Errorf("tms reset: %w", err)
		}
		if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
			"tms_source_node_name":        "",
			"tms_source_node_id":          0,
			"central_tms_context_id":      nil,
			"cas_engine_destination_name": "",
		}).Error; err != nil {
			return fmt.Errorf("tms reset: clear TMS product fields: %w", err)
		}

		// Defensive: mark any stale running TMS jobs as failed.
		now := time.Now()
		if err := tx.Model(&db.TenantBootstrapJob{}).
			Where("cpi_tenant_id = ? AND layer = ? AND state = ?", tenantID, lifecycle.LayerTMS, lifecycle.JobRunning).
			Updates(map[string]any{
				"state":        lifecycle.JobFailed,
				"failure_type": lifecycle.FailureRemoteSystemError,
				"ended_at":     &now,
				"error_detail": "reset by operator",
			}).Error; err != nil {
			return fmt.Errorf("tms reset: mark stale jobs failed: %w", err)
		}

		return nil
	})
}

// resetTmsPrereqs clears TMS layer detail statuses to missing and sets tms_blocking_reason.
// Called by ApplyTmsExtension (Phase 3, reason="") and ResetTmsExtension (reason="OPERATOR_RESET").
func resetTmsPrereqs(tx *gorm.DB, tenantID uint, blockingReason string) error {
	return tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
		"tms_blocking_reason":              blockingReason,
		"cas_application_status":           lifecycle.PrereqMissing,
		"cas_standard_status":              lifecycle.PrereqMissing,
		"cloud_integration_dest_status":    lifecycle.PrereqMissing,
		"content_assembly_dest_status":     lifecycle.PrereqMissing,
		"transport_management_dest_status": lifecycle.PrereqMissing,
	}).Error
}

// failTmsPhase2 atomically rolls back TmsExtensionState and writes tms_blocking_reason
// when Phase 2 (synchronous inspect) fails.
func (s *Service) failTmsPhase2(tenantID uint, reason string) {
	_ = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := s.transitionTmsWithTx(tx, tenantID, TmsEventApplyFailed); err != nil {
			return err
		}
		return tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).
			Update("tms_blocking_reason", reason).Error
	})
}

// inspectTmsExtension checks TMS-specific resources (CAS, destination/lite, subscriber destinations).
func (s *Service) inspectTmsExtension(ctx context.Context, inspector *TenantInspector, tenant *db.CpiTenant) (*InspectionResult, error) {
	result := &InspectionResult{
		ServiceKeyGUIDs:   make(map[string]string),
		DestinationExists: make(map[string]bool),
	}

	orgGUID := ""
	if space, err := inspector.cfClient.GetSpace(ctx, tenant.CfSpace); err == nil && space != nil {
		if space.Relationships.Organization != nil && space.Relationships.Organization.Data != nil {
			orgGUID = space.Relationships.Organization.Data.GUID
		}
	}
	result.OrgGUID = orgGUID

	// Discover existing PIR instance (created by BASE bootstrap).
	// TMS extension doesn't create it, but needs the key GUID for CloudIntegration destination.
	if err := inspector.checkServiceInstance(ctx, orgGUID, tenant.CfSpace, offeringPIR, planPirApi,
		instanceNamePirApi, missingCodePirApi, &result.PirApiInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspect tms: %s (PIR discovery): %w", StepCheckPirApi, err)
	}

	// CHECK_DESTINATION_SERVICE (destination/lite instance)
	if err := inspector.checkServiceInstance(ctx, orgGUID, tenant.CfSpace, offeringDestination, planDestinationLite,
		instanceNameDestinationLite, missingCodeDestinationService, &result.DestinationServiceInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspect tms: %s: %w", StepCheckDestinationService, err)
	}

	// CHECK_CAS_APPLICATION
	if err := inspector.checkServiceInstance(ctx, orgGUID, tenant.CfSpace, offeringCAS, planCasApplication,
		instanceNameCasApplication, missingCodeCasApplication, &result.CasApplicationInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspect tms: %s: %w", StepCheckCasApplication, err)
	}

	// CHECK_CAS_STANDARD
	if err := inspector.checkServiceInstance(ctx, orgGUID, tenant.CfSpace, offeringCAS, planCasStandard,
		instanceNameCasStandard, missingCodeCasStandard, &result.CasStandardInstanceGUID, result); err != nil {
		return result, fmt.Errorf("inspect tms: %s: %w", StepCheckCasStandard, err)
	}

	// CHECK_SERVICE_KEYS (CAS keys)
	if err := inspector.checkServiceKeys(ctx, result); err != nil {
		return result, fmt.Errorf("inspect tms: %s: %w", StepCheckServiceKeys, err)
	}

	// CHECK_DESTINATIONS (subscriber-side: CloudIntegration, ContentAssemblyService, TransportManagementService)
	if result.DestinationServiceInstanceGUID != "" {
		if err := inspector.checkDestinations(ctx, result); err != nil {
			return result, fmt.Errorf("inspect tms: %s: %w", StepCheckDestinations, err)
		}
	}

	return result, nil
}

// runTmsExtensionBootstrap is the async goroutine for TMS extension bootstrap.
func (s *Service) runTmsExtensionBootstrap(tenant *db.CpiTenant, jobID uint, cfToken string, result *InspectionResult) {
	ctx := context.Background()

	fail := func(failureType lifecycle.BootstrapFailureType, step, reason string) {
		state := lifecycle.JobFailed
		if failureType == lifecycle.FailureWaitingUserAction {
			state = lifecycle.JobWaitingUserAction
		}
		now := time.Now()
		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).Updates(map[string]any{
				"state":        state,
				"failure_type": failureType,
				"current_step": step,
				"ended_at":     &now,
				"error_detail": reason,
			}).Error; err != nil {
				return err
			}
			return s.transitionTmsWithTx(tx, tenant.ID, TmsEventApplyFailed)
		}); err != nil {
			s.L(ctx).Errorw("tms bootstrap: failed to record failure",
				"component", "tms-bootstrap", "tenant_id", tenant.ID, "jobID", jobID, "error", err)
			return // tx failed — don't write blocking_reason on a tenant still stuck in bootstrapping
		}
		// blocking_reason is display-only; persist best-effort outside the transaction
		// so its failure cannot roll back the critical state writes above.
		_ = s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
			Update("tms_blocking_reason", reason)
	}

	finish := func() {
		now := time.Now()
		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).Updates(map[string]any{
				"state":    lifecycle.JobFinished,
				"ended_at": &now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
				Update("tms_blocking_reason", "").Error; err != nil {
				return err
			}
			return s.transitionTmsWithTx(tx, tenant.ID, TmsEventApplyFinished)
		}); err != nil {
			s.L(ctx).Errorw("tms bootstrap: failed to record completion",
				"component", "tms-bootstrap", "tenant_id", tenant.ID, "jobID", jobID, "error", err)
		}
	}

	setStep := func(step string) {
		_ = s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
			Update("current_step", step)
	}

	markReady := func(field string) {
		_ = s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
			Update(field, lifecycle.PrereqReady)
	}

	// ── Build applier ────────────────────────────────────────────────────────

	bootstrapper, err := newBootstrapApplier(tenant, cfToken, result, s.ProviderDest)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, "", fmt.Sprintf("build applier: %s", err))
		return
	}

	var credentialActions []credentialAction

	// ── CHECK_CAS_APPLICATION ────────────────────────────────────────────────

	setStep(StepCheckCasApplication)
	acts, err := bootstrapper.ensureInstanceAndKey(ctx, offeringCAS, planCasApplication,
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

	// ── CHECK_CAS_STANDARD ───────────────────────────────────────────────────

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

	// ── CHECK_DESTINATION_SERVICE (destination/lite instance) ─────────────────

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

	// ── CHECK_DESTINATIONS (subscriber-side) ─────────────────────────────────
	// Load Central TMS context NOW — state is bootstrapping, so UpsertCentralTmsContext
	// is blocked (409).  Reading here instead of ApplyTmsExtension eliminates the
	// race window where Central TMS could change between the read and the transition
	// to bootstrapping.
	setStep(StepCheckDestinations)
	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckDestinations,
			fmt.Sprintf("load Central TMS context: %s", err.Error()))
		return
	}
	destActs, err := bootstrapper.ensureSubscriberDestinations(ctx, tenant, result, tmsCtx)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepCheckDestinations, err.Error())
		return
	}
	credentialActions = append(credentialActions, destActs...)
	if result.PirApiInstanceGUID != "" {
		markReady("cloud_integration_dest_status")
	}
	if result.CasStandardInstanceGUID != "" {
		markReady("content_assembly_dest_status")
	}
	if tmsCtx != nil && s.ProviderDest != nil {
		markReady("transport_management_dest_status")
	}

	// ── ENSURE_CAS_PROVIDER_DEST ─────────────────────────────────────────────

	setStep(StepEnsureCasProviderDest)
	if s.ProviderDest == nil {
		fail(lifecycle.FailureRemoteSystemError, StepEnsureCasProviderDest,
			"ProviderDest is nil — provider Destination Service client was not injected at startup")
		return
	}
	casActs, err := bootstrapper.ensureCasProviderDestination(ctx, tenant, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepEnsureCasProviderDest, err.Error())
		return
	}
	credentialActions = append(credentialActions, casActs...)

	// Persist credential actions (audit-only).
	actsJSON, _ := json.Marshal(credentialActions)
	_ = s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
		Update("credential_actions", actsJSON)

	// Persist CAS engine destination name on tenant.
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
		Update("cas_engine_destination_name", tenant.CasEngineDestinationName).Error; err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepEnsureCasProviderDest,
			fmt.Sprintf("persist CAS destination name: %s", err))
		return
	}

	finish()
}

// ensureCasProviderDestination writes CPIDELIVERY_CAS_{id} into the provider-side
// Destination Service.
func (b *bootstrapApplier) ensureCasProviderDestination(ctx context.Context, tenant *db.CpiTenant, result *InspectionResult) ([]credentialAction, error) {
	if b.providerDest == nil {
		return nil, fmt.Errorf("ensureCasProviderDestination: ProviderDest is nil")
	}

	tenant.CasEngineDestinationName = fmt.Sprintf("CPIDELIVERY_CAS_%d", tenant.ID)

	if result.CasApplicationInstanceGUID == "" {
		return nil, fmt.Errorf("ensureCasProviderDestination: CAS application instance not found")
	}
	casKeyGUID, ok := result.ServiceKeyGUIDs[result.CasApplicationInstanceGUID]
	if !ok {
		return nil, fmt.Errorf("ensureCasProviderDestination: no service key GUID for CAS instance %q", result.CasApplicationInstanceGUID)
	}
	casCreds, err := b.cfClient.GetServiceKeyCredentials(ctx, casKeyGUID)
	if err != nil {
		return nil, fmt.Errorf("get CAS application key credentials for provider dest: %w", err)
	}
	casDest, err := buildOAuthDestination(tenant.CasEngineDestinationName, nil, tenant.ID, casCreds)
	if err != nil {
		return nil, err
	}
	if err := b.providerDest.UpsertDestination(ctx, casDest); err != nil {
		return nil, fmt.Errorf("upsert provider dest %q: %w", casDest.Name, err)
	}

	return []credentialAction{{DestinationName: casDest.Name, ActionType: "upserted"}}, nil
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
// All three destinations are mandatory for TMS extension bootstrap.
// Prerequisites are guaranteed by upstream ensure steps and precondition checks:
//   - PirApiInstanceGUID + key: BASE bootstrap (IsOnboarded) + inspect discovery
//   - CasStandardInstanceGUID + key: ensureInstanceAndKey(CAS standard) in goroutine
//   - tmsCtx: loaded inside goroutine under bootstrapping mutual exclusion
//
// This function validates all prerequisites and returns an error if any are
// missing, serving as a final safety net against upstream regressions.
func (b *bootstrapApplier) buildSubscriberDestinations(ctx context.Context, result *InspectionResult, tenant *db.CpiTenant, tmsCtx *db.CentralTmsContext) ([]cf.Destination, error) {
	var dests []cf.Destination

	// CloudIntegration — PIR api service key credentials.
	if result.PirApiInstanceGUID == "" {
		return nil, fmt.Errorf("PIR API instance GUID is empty — BASE bootstrap may be incomplete")
	}
	pirKeyGUID, ok := result.ServiceKeyGUIDs[result.PirApiInstanceGUID]
	if !ok {
		return nil, fmt.Errorf("no service key GUID for PIR api instance %q", result.PirApiInstanceGUID)
	}
	pirCreds, err := b.cfClient.GetServiceKeyCredentials(ctx, pirKeyGUID)
	if err != nil {
		return nil, fmt.Errorf("get PIR api key credentials: %w", err)
	}
	ciDest, err := buildOAuthDestination("CloudIntegration", transportModuleURL, tenant.ID, pirCreds)
	if err != nil {
		return nil, err
	}
	dests = append(dests, ciDest)

	// ContentAssemblyService — CAS standard service key credentials.
	if result.CasStandardInstanceGUID == "" {
		return nil, fmt.Errorf("CAS standard instance GUID is empty — ensureInstanceAndKey should have created it")
	}
	casKeyGUID, ok := result.ServiceKeyGUIDs[result.CasStandardInstanceGUID]
	if !ok {
		return nil, fmt.Errorf("no service key GUID for CAS standard instance %q", result.CasStandardInstanceGUID)
	}
	casCreds, err := b.cfClient.GetServiceKeyCredentials(ctx, casKeyGUID)
	if err != nil {
		return nil, fmt.Errorf("get CAS standard key credentials: %w", err)
	}
	casDest, err := buildOAuthDestination("ContentAssemblyService", nil, tenant.ID, casCreds)
	if err != nil {
		return nil, err
	}
	dests = append(dests, casDest)

	// TransportManagementService — TMS OAuth credentials copied from provider-side.
	if tmsCtx == nil || tmsCtx.TmsApiDestinationName == "" {
		return nil, fmt.Errorf("Central TMS context not configured — cannot create TransportManagementService destination")
	}
	if b.providerDest == nil {
		return nil, fmt.Errorf("provider Destination Service client is nil — cannot resolve TMS credentials")
	}
	if tenant.TmsSourceNodeName == "" {
		return nil, fmt.Errorf("TmsSourceNodeName is empty — node selection is incomplete")
	}
	tmsDest, err := b.providerDest.GetDestination(ctx, tmsCtx.TmsApiDestinationName)
	if err != nil {
		return nil, fmt.Errorf("get TMS API destination %q from provider: %w", tmsCtx.TmsApiDestinationName, err)
	}
	if tmsDest == nil {
		return nil, fmt.Errorf("TMS API destination %q not found in provider Destination Service", tmsCtx.TmsApiDestinationName)
	}
	tmsSubDest := *tmsDest
	tmsSubDest.Name = "TransportManagementService"
	tmsSubDest.Description = fmt.Sprintf("DO NOT MODIFY. TransportManagementService for tenant %d, sourceSystemId=%s. Created by cpi-delivery bootstrap.", tenant.ID, tenant.TmsSourceNodeName)
	tmsSubDest.SourceSystemId = tenant.TmsSourceNodeName
	dests = append(dests, tmsSubDest)

	return dests, nil
}
