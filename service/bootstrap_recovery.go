package service

import (
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"
)

// RecoverStuckBootstraps fails over tenants stuck in running bootstrap states.
//
// At startup, no bootstrap goroutines are alive — any tenant in readying or
// bootstrapping is stuck because the goroutine was killed by the previous
// process exit (deploy, OOM, crash). This function:
//  1. Marks all orphaned running jobs as failed.
//  2. Transitions readying → configured (BASE).
//  3. Transitions bootstrapping → node_confirmed (TMS).
//
// Pattern follows sync_tracker.go:RecoverActiveSyncs. Called once from main.go
// after data migrations, before the HTTP server starts.
func (s *Service) RecoverStuckBootstraps() {
	now := time.Now()

	// 1. Mark all orphaned running jobs as failed.
	// At startup no goroutines are running, so every running job is orphaned.
	if result := s.DB.Model(&db.TenantBootstrapJob{}).
		Where("state = ?", lifecycle.JobRunning).
		Updates(map[string]any{
			"state":        lifecycle.JobFailed,
			"failure_type": lifecycle.FailureRemoteSystemError,
			"ended_at":     &now,
			"error_detail": "bootstrap interrupted by application restart",
		}); result.Error != nil {
		s.Logger.Errorw("RecoverStuckBootstraps: failed to mark orphaned jobs",
			"component", "bootstrap-recovery", "error", result.Error)
	} else if result.RowsAffected > 0 {
		s.Logger.Warnw("RecoverStuckBootstraps: marked orphaned jobs as failed",
			"component", "bootstrap-recovery", "count", result.RowsAffected)
	}

	// 2. BASE: readying → configured.
	if result := s.DB.Model(&db.CpiTenant{}).
		Where("lifecycle_state = ?", lifecycle.TenantReadying).
		Updates(map[string]any{
			"lifecycle_state": lifecycle.TenantConfigured,
			"blocking_reason": lifecycle.ReasonBootstrapInterrupted,
		}); result.Error != nil {
		s.Logger.Errorw("RecoverStuckBootstraps: failed to recover readying tenants",
			"component", "bootstrap-recovery", "error", result.Error)
	} else if result.RowsAffected > 0 {
		s.Logger.Warnw("RecoverStuckBootstraps: recovered stuck BASE bootstrap tenants",
			"component", "bootstrap-recovery", "count", result.RowsAffected)
	}

	// 3. TMS: bootstrapping → node_confirmed.
	if result := s.DB.Model(&db.CpiTenant{}).
		Where("tms_extension_state = ?", lifecycle.TmsBootstrapping).
		Updates(map[string]any{
			"tms_extension_state": lifecycle.TmsNodeConfirmed,
			"tms_blocking_reason": lifecycle.ReasonBootstrapInterrupted,
		}); result.Error != nil {
		s.Logger.Errorw("RecoverStuckBootstraps: failed to recover bootstrapping tenants",
			"component", "bootstrap-recovery", "error", result.Error)
	} else if result.RowsAffected > 0 {
		s.Logger.Warnw("RecoverStuckBootstraps: recovered stuck TMS bootstrap tenants",
			"component", "bootstrap-recovery", "count", result.RowsAffected)
	}
}
