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

	// Preconditions: tenant must be onboarded and Central TMS must be configured.
	if !tenant.IsOnboarded() {
		return 0, fmt.Errorf("tms apply: tenant %d is not onboarded (LifecycleState=%s)", tenantID, tenant.LifecycleState)
	}
	tmsCtx, err := loadTmsContext(s.DB)
	if err != nil {
		return 0, fmt.Errorf("tms apply: %w", err)
	}

	jobType := lifecycle.JobTypeApply
	if tenant.TmsExtensionState == lifecycle.TmsBootstrapFailed {
		jobType = lifecycle.JobTypeRetry
	}

	// Atomically claim bootstrapping state + guard against concurrent BASE job.
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var t db.CpiTenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "lifecycle_state", "tms_extension_state").
			First(&t, tenantID).Error; err != nil {
			return err
		}
		if t.LifecycleState == lifecycle.TenantReadying {
			return fmt.Errorf("tenant bootstrap is running; only one bootstrap job at a time")
		}
		// Reset TMS extension detail statuses.
		if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
			"tms_blocking_reason":              "",
			"cas_application_status":           lifecycle.PrereqMissing,
			"cas_standard_status":              lifecycle.PrereqMissing,
			"cloud_integration_dest_status":    lifecycle.PrereqMissing,
			"content_assembly_dest_status":     lifecycle.PrereqMissing,
			"transport_management_dest_status": lifecycle.PrereqMissing,
		}).Error; err != nil {
			return err
		}
		return s.transitionTmsWithTx(tx, tenantID, TmsEventApplyStarted)
	}); err != nil {
		return 0, fmt.Errorf("tms apply: %w", err)
	}

	// Synchronous inspect (TMS extension resources).
	inspector, err := newInspector(tenant, cfToken)
	if err != nil {
		_ = s.TransitionTms(tenantID, TmsEventApplyFailed)
		return 0, fmt.Errorf("tms apply: build inspector: %w", err)
	}
	result, err := s.inspectTmsExtension(ctx, inspector, tenant)
	if err != nil {
		_ = s.TransitionTms(tenantID, TmsEventApplyFailed)
		return 0, fmt.Errorf("tms apply: inspect: %w", err)
	}
	if len(result.PermissionIssues) > 0 {
		_ = s.TransitionTms(tenantID, TmsEventApplyFailed)
		return 0, fmt.Errorf("tms apply: permission issues: %v", result.PermissionIssues)
	}
	if len(result.WaitingUserAction) > 0 {
		_ = s.TransitionTms(tenantID, TmsEventApplyFailed)
		return 0, fmt.Errorf("tms apply: waiting user action: %v", result.WaitingUserAction)
	}

	// Create TMS job and launch goroutine.
	missingJSON, _ := json.Marshal(result.MissingItems)
	job := &db.TenantBootstrapJob{
		CpiTenantID:          tenantID,
		Layer:                lifecycle.LayerTMS,
		JobType:              jobType,
		State:                lifecycle.JobRunning,
		MissingPrerequisites: missingJSON,
		StartedAt:            time.Now(),
	}
	if err := s.DB.Create(job).Error; err != nil {
		_ = s.TransitionTms(tenantID, TmsEventApplyFailed)
		return 0, fmt.Errorf("tms apply: create job: %w", err)
	}

	go s.runTmsExtensionBootstrap(tenant, job.ID, cfToken, result, tmsCtx)
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

		// Clear all TMS product fields (same set as SaveCfIdentity Rule 1).
		if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
			"tms_blocking_reason":              "OPERATOR_RESET",
			"tms_source_node_name":             "",
			"tms_source_node_id":               0,
			"central_tms_context_id":           nil,
			"cas_engine_destination_name":      "",
			"cas_application_status":           lifecycle.PrereqMissing,
			"cas_standard_status":              lifecycle.PrereqMissing,
			"cloud_integration_dest_status":    lifecycle.PrereqMissing,
			"content_assembly_dest_status":     lifecycle.PrereqMissing,
			"transport_management_dest_status": lifecycle.PrereqMissing,
		}).Error; err != nil {
			return fmt.Errorf("tms reset: clear TMS fields: %w", err)
		}

		// Defensive: mark any stale running TMS jobs as failed.
		now := time.Now()
		if err := tx.Model(&db.TenantBootstrapJob{}).
			Where("cpi_tenant_id = ? AND layer = ? AND state = ?", tenantID, lifecycle.LayerTMS, lifecycle.JobRunning).
			Updates(map[string]any{
				"state":        lifecycle.JobFailed,
				"failure_type": lifecycle.FailureRemoteSystemError,
				"ended_at":     &now,
			}).Error; err != nil {
			return fmt.Errorf("tms reset: mark stale jobs failed: %w", err)
		}

		return nil
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
func (s *Service) runTmsExtensionBootstrap(tenant *db.CpiTenant, jobID uint, cfToken string, result *InspectionResult, tmsCtx *db.CentralTmsContext) {
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
		}
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

	setStep(StepCheckDestinations)
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
