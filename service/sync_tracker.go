package service

import (
	"context"
	"sync"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

// SyncTracker manages per-DR sync goroutines.
// Each active DR gets its own goroutine that periodically calls SyncDeliveryStatus
// until the DR reaches a terminal state.
type SyncTracker struct {
	mu      sync.Mutex
	running map[uint]context.CancelFunc
}

func NewSyncTracker() *SyncTracker {
	return &SyncTracker{
		running: make(map[uint]context.CancelFunc),
	}
}

// TryStart attempts to start tracking a DR. Returns a context and true if started.
// If the DR is already being tracked, returns nil and false (idempotent).
func (t *SyncTracker) TryStart(drID uint) (context.Context, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.running[drID]; ok {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.running[drID] = cancel
	return ctx, true
}

// Finish marks a DR sync as done (natural exit). Calls cancel to release context resources.
func (t *SyncTracker) Finish(drID uint) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cancel, ok := t.running[drID]; ok {
		cancel()
		delete(t.running, drID)
	}
}

// StopAll cancels all running goroutines (for graceful shutdown).
func (t *SyncTracker) StopAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for drID, cancel := range t.running {
		cancel()
		delete(t.running, drID)
	}
}

// ActiveCount returns the number of currently running sync goroutines.
func (t *SyncTracker) ActiveCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.running)
}

// StartDRSync starts a per-DR sync goroutine that immediately performs one sync,
// then continues polling every 15s until the DR reaches a terminal state.
// Idempotent: if already running, returns immediately.
// No-op if SyncTracker is nil (e.g. in tests).
func (s *Service) StartDRSync(drID uint) {
	if s.SyncTracker == nil {
		return
	}
	ctx, started := s.SyncTracker.TryStart(drID)
	if !started {
		s.Logger.Debugw("already running, skipped", "component", "sync_tracker", "dr_id", drID)
		return
	}
	s.Logger.Infow("started", "component", "sync_tracker", "dr_id", drID)
	go s.runDRSync(ctx, drID)
}

// hasActiveOps checks if any op in the DR is currently being processed by TMS/CPI.
func (s *Service) hasActiveOps(drID uint) bool {
	var count int64
	if err := s.DB.Model(&db.ArtifactTenantOperation{}).
		Where("delivery_request_id = ?", drID).
		Where("import_state = ? OR deploy_state = ?",
			lifecycle.ImportInProgress, lifecycle.DeployInProgress).
		Count(&count).Error; err != nil {
		s.Logger.Errorw("hasActiveOps query failed, keeping alive", "component", "sync_tracker", "dr_id", drID, "error", err)
		return true // assume active on error — avoid premature exit
	}
	return count > 0
}

// runDRSync will trigger a sync immediately, then every 15s until there are
// no more active (InProgress) operations to track.
func (s *Service) runDRSync(ctx context.Context, drID uint) {
	defer func() {
		s.L(ctx).Infow("goroutine exiting", "component", "sync_tracker", "dr_id", drID)
		s.SyncTracker.Finish(drID)
	}()

	// Immediate first sync — don't wait for the first tick.
	s.L(ctx).Debugw("immediate sync", "component", "sync_tracker", "dr_id", drID)
	_ = s.SyncDeliveryStatus(ctx, drID, "system")
	if !s.hasActiveOps(drID) {
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.L(ctx).Debugw("context cancelled", "component", "sync_tracker", "dr_id", drID)
			return
		case <-ticker.C:
			s.L(ctx).Debugw("tick sync", "component", "sync_tracker", "dr_id", drID)
			_ = s.SyncDeliveryStatus(ctx, drID, "system")
			if !s.hasActiveOps(drID) {
				s.L(ctx).Infow("no active ops, stopping", "component", "sync_tracker", "dr_id", drID)
				return
			}
		}
	}
}

// RecoverActiveSyncs starts sync goroutines for DRs that have active (InProgress)
// operations. Called once at service startup to resume polling for operations that
// were being tracked before restart.
func (s *Service) RecoverActiveSyncs() {
	var drIDs []uint
	if err := s.DB.Model(&db.ArtifactTenantOperation{}).
		Joins("JOIN delivery_requests ON delivery_requests.id = artifact_tenant_operations.delivery_request_id AND delivery_requests.deleted_at IS NULL").
		Where("import_state = ? OR deploy_state = ?",
			lifecycle.ImportInProgress, lifecycle.DeployInProgress).
		Distinct("delivery_request_id").
		Pluck("delivery_request_id", &drIDs).Error; err != nil {
		s.Logger.Errorw("failed to query DRs with active ops", "component", "sync_tracker", "error", err)
		return
	}
	for _, drID := range drIDs {
		s.StartDRSync(drID)
	}
	if len(drIDs) > 0 {
		s.Logger.Infow("recovered active DR sync goroutines", "component", "sync_tracker", "count", len(drIDs))
	}
}
