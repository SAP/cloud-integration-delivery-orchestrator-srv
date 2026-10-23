package service

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
	// Guard: reject if TMS extension bootstrap is running (only one job at a time).
	// Then transition lifecycle to readying via the same SELECT FOR UPDATE.
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		var t db.CpiTenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "lifecycle_state", "tms_extension_state").
			First(&t, tenantID).Error; err != nil {
			return err
		}
		if t.TmsExtensionState == lifecycle.TmsBootstrapping {
			return fmt.Errorf("TMS extension bootstrap is running; only one bootstrap job at a time")
		}
		return s.transitionLifecycleWithTx(tx, tenantID, EventBootstrapStarted)
	}); err != nil {
		return 0, fmt.Errorf("apply: %w", err)
	}
	// Reset only base bootstrap prerequisite status; TMS extension statuses are untouched.
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
		"blocking_reason": "",
		"pir_api_status":  lifecycle.PrereqMissing,
	}).Error; err != nil {
		s.L(ctx).Warnw("failed to reset prereq statuses", "component", "bootstrap", "tenant_id", tenantID, "error", err)
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
		Layer:                lifecycle.LayerBase,
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

// GetBootstrapStatus returns the most recent BASE TenantBootstrapJob for the tenant.
func (s *Service) GetBootstrapStatus(tenantID uint) (*db.TenantBootstrapJob, error) {
	var job db.TenantBootstrapJob
	err := s.DB.Where("cpi_tenant_id = ? AND layer = ?", tenantID, lifecycle.LayerBase).
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
// ResetBootstrap rolls the tenant's base bootstrap back to configured state,
// clearing all bootstrap product fields (PIR status, URL, destination name).
//
// Allowed from: configured (idempotent), not_ready, ready.
// Rejected by state machine for: draft (no EventReset edge), readying (no EventReset edge).
//
// All writes are atomic in a single transaction. The state transition runs
// first (SELECT FOR UPDATE), so readying/draft are rejected before any
// field cleanup or job marking occurs.
func (s *Service) ResetBootstrap(tenantID uint) error {
	return s.DB.Transaction(func(tx *gorm.DB) error {
		// State transition first: validates state + acquires row lock.
		// draft/readying are rejected here → rollback, zero side effects.
		if err := s.transitionLifecycleWithTx(tx, tenantID, EventReset); err != nil {
			return fmt.Errorf("reset: %w", err)
		}

		// Clear bootstrap product fields.
		if err := tx.Model(&db.CpiTenant{}).Where("id = ?", tenantID).Updates(map[string]any{
			"blocking_reason":          "OPERATOR_RESET",
			"pir_api_status":           lifecycle.PrereqMissing,
			"pir_api_url":              "",
			"pir_api_destination_name": "",
		}).Error; err != nil {
			return fmt.Errorf("reset: clear bootstrap fields: %w", err)
		}

		// Defensive: mark any stale running BASE jobs as failed.
		now := time.Now()
		if err := tx.Model(&db.TenantBootstrapJob{}).
			Where("cpi_tenant_id = ? AND layer = ? AND state = ?", tenantID, lifecycle.LayerBase, lifecycle.JobRunning).
			Updates(map[string]any{
				"state":        lifecycle.JobFailed,
				"failure_type": lifecycle.FailureRemoteSystemError,
				"ended_at":     &now,
			}).Error; err != nil {
			return fmt.Errorf("reset: mark stale jobs failed: %w", err)
		}

		return nil
	})
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
			s.L(ctx).Errorw("failed to record job failure; tenant may be stuck in readying",
				"component", "bootstrap", "tenant_id", tenant.ID, "jobID", jobID, "error", err)
		}
		// blocking_reason is display-only; persist best-effort outside the
		// transaction so its failure cannot roll back the critical state writes above.
		if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
			Update("blocking_reason", reason).Error; err != nil {
			s.L(ctx).Errorw("failed to persist blocking_reason",
				"component", "bootstrap", "tenant_id", tenant.ID, "jobID", jobID, "error", err)
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
			s.L(ctx).Errorw("failed to record job completion; tenant may be stuck in readying",
				"component", "bootstrap", "tenant_id", tenant.ID, "jobID", jobID, "error", err)
		}
	}

	setStep := func(step string) {
		if err := s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
			Update("current_step", step).Error; err != nil {
			s.L(ctx).Errorw("failed to update job step",
				"component", "bootstrap", "tenant_id", tenant.ID, "jobID", jobID, "step", step, "error", err)
		}
	}

	markReady := func(field string) {
		if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
			Update(field, lifecycle.PrereqReady).Error; err != nil {
			s.L(ctx).Errorw("failed to update prereq status",
				"component", "bootstrap", "tenant_id", tenant.ID, "field", field, "error", err)
		}
	}

	// ── Apply missing items ───────────────────────────────────────────────────

	bootstrapper, err := newBootstrapApplier(tenant, cfToken, result, s.ProviderDest)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, "", fmt.Sprintf("build applier: %s", err))
		return
	}

	var credentialActions []credentialAction

	// ── BASE bootstrap: PIR only ─────────────────────────────────────────────

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

	// Rule 3 (RFC 032 §5.4): if PIR key was newly created (old key deleted externally),
	// the subscriber-side CloudIntegration destination holds stale credentials.
	// Attempt to invalidate TMS extension — the state machine (SELECT FOR UPDATE)
	// checks the real current state; if it's not bootstrapped, no action is needed
	// (no stale destination exists in earlier states).
	pirKeyCreated := false
	for _, a := range acts {
		if a.ActionType == "created" {
			pirKeyCreated = true
			break
		}
	}
	if pirKeyCreated {
		if err := s.TransitionTms(tenant.ID, TmsEventInvalidated); err != nil {
			// State doesn't support invalidation (e.g., not_started, node_pending) —
			// no stale CloudIntegration destination exists. Safe to ignore.
			s.L(ctx).Debugw("rule 3: PIR key recreated but TMS invalidation not applicable",
				"component", "bootstrap", "tenant_id", tenant.ID, "info", err)
		} else {
			_ = s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).
				Update("tms_blocking_reason", lifecycle.TmsReasonPirKeyChanged)
		}
	}

	// ENSURE_PIR_PROVIDER_DEST → write CPIDELIVERY_PIR_{id} into provider Destination Service
	setStep(StepEnsurePirProviderDest)
	if s.ProviderDest == nil {
		fail(lifecycle.FailureRemoteSystemError, StepEnsurePirProviderDest,
			"ProviderDest is nil — provider Destination Service client was not injected at startup")
		return
	}
	provActs, err := bootstrapper.ensurePirProviderDestination(ctx, tenant, result)
	if err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepEnsurePirProviderDest, err.Error())
		return
	}
	credentialActions = append(credentialActions, provActs...)

	// Persist credential action log (audit-only; failure is best-effort).
	actsJSON, _ := json.Marshal(credentialActions)
	if err := s.DB.Model(&db.TenantBootstrapJob{}).Where("id = ?", jobID).
		Update("credential_actions", actsJSON).Error; err != nil {
		s.L(ctx).Errorw("failed to persist credential_actions",
			"component", "bootstrap", "tenant_id", tenant.ID, "jobID", jobID, "error", err)
	}

	// Persist provider-side PIR destination name on tenant.
	persistUpdates := map[string]any{
		"pir_api_destination_name": tenant.PirApiDestinationName,
		"pir_api_url":              tenant.PirApiUrl,
	}
	if err := s.DB.Model(&db.CpiTenant{}).Where("id = ?", tenant.ID).Updates(persistUpdates).Error; err != nil {
		fail(lifecycle.FailureRemoteSystemError, StepEnsurePirProviderDest,
			fmt.Sprintf("persist destination names: %s", err))
		return
	}

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
// ensurePirProviderDestination writes CPIDELIVERY_PIR_{id} into the provider-side
// Destination Service.  This is the only provider destination written by the BASE
// bootstrap; CPIDELIVERY_CAS_{id} is written by the TMS extension bootstrap.
func (b *bootstrapApplier) ensurePirProviderDestination(ctx context.Context, tenant *db.CpiTenant, result *InspectionResult) ([]credentialAction, error) {
	if b.providerDest == nil {
		return nil, fmt.Errorf("ensurePirProviderDestination: ProviderDest is nil")
	}

	tenant.PirApiDestinationName = fmt.Sprintf("CPIDELIVERY_PIR_%d", tenant.ID)

	if result.PirApiInstanceGUID == "" {
		return nil, fmt.Errorf("ensurePirProviderDestination: PIR API instance not found")
	}
	pirKeyGUID, ok := result.ServiceKeyGUIDs[result.PirApiInstanceGUID]
	if !ok {
		return nil, fmt.Errorf("ensurePirProviderDestination: no service key GUID for PIR instance %q", result.PirApiInstanceGUID)
	}
	pirCreds, err := b.cfClient.GetServiceKeyCredentials(ctx, pirKeyGUID)
	if err != nil {
		return nil, fmt.Errorf("get PIR api key credentials for provider dest: %w", err)
	}
	pirDest, err := buildOAuthDestination(tenant.PirApiDestinationName, nil, tenant.ID, pirCreds)
	if err != nil {
		return nil, err
	}
	if err := b.providerDest.UpsertDestination(ctx, pirDest); err != nil {
		return nil, fmt.Errorf("upsert provider dest %q: %w", pirDest.Name, err)
	}
	if pirDest.URL == "" {
		return nil, fmt.Errorf("ensurePirProviderDestination: PIR destination %q has empty URL", pirDest.Name)
	}
	tenant.PirApiUrl = pirDest.URL

	return []credentialAction{{DestinationName: pirDest.Name, ActionType: "upserted"}}, nil
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
		Description:         fmt.Sprintf("DO NOT MODIFY. %s for tenant %d. Created by cpi-delivery bootstrap.", name, tenantID),
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
