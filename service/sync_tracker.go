package service

import (
	"context"
	"sync"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

// TerminalDRStatuses defines aggregate statuses where the DR lifecycle is complete
// and no further TMS/CPI polling is needed. The sync goroutine exits when the DR
// reaches one of these states.
//
// Non-terminal states (AWAITING_IMPORT, IMPORTING, DEPLOYING, etc.) keep the
// goroutine alive because TMS/CPI may still be processing (even if TMS temporarily
// reports "INITIAL" due to processing lag).
var TerminalDRStatuses = []lifecycle.AggregateStatus{
	lifecycle.AggDeployed,
	lifecycle.AggCanceled,
	lifecycle.AggImportFailed,
	lifecycle.AggDeployFailed,
	lifecycle.AggWaitingDeploy,
}

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
		s.Logger.Debugf("[SyncTracker] drId=%d already running, skipped", drID)
		return
	}
	s.Logger.Infof("[SyncTracker] drId=%d started", drID)
	go s.runDRSync(ctx, drID)
}

// runDRSync will trigger a sync immediately, then every 15s until the DR reaches a terminal state.
func (s *Service) runDRSync(ctx context.Context, drID uint) {
	defer func() {
		s.Logger.Infof("[SyncTracker] drId=%d goroutine exiting", drID)
		s.SyncTracker.Finish(drID)
	}()

	// Immediate first sync — don't wait for the first tick.
	s.Logger.Debugf("[SyncTracker] drId=%d immediate sync", drID)
	_ = s.SyncDeliveryStatus(drID, "system")
	if s.isDRTerminal(drID) {
		s.Logger.Infof("[SyncTracker] drId=%d reached terminal state after first sync", drID)
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.Logger.Debugf("[SyncTracker] drId=%d context cancelled", drID)
			return
		case <-ticker.C:
			s.Logger.Debugf("[SyncTracker] drId=%d tick sync", drID)
			_ = s.SyncDeliveryStatus(drID, "system")
			if s.isDRTerminal(drID) {
				s.Logger.Infof("[SyncTracker] drId=%d reached terminal state", drID)
				return
			}
		}
	}
}

// isDRTerminal checks whether the DR has reached a terminal state (deployed, canceled, or failed).
// Only terminal states cause the sync goroutine to exit. Non-terminal states (including
// AWAITING_IMPORT where TMS may have regressed the op state) keep the goroutine alive.
func (s *Service) isDRTerminal(drID uint) bool {
	var statuses []lifecycle.AggregateStatus
	s.DB.Model(&db.DeliveryRequest{}).
		Where("id = ?", drID).
		Pluck("aggregate_status", &statuses)
	if len(statuses) == 0 {
		return true // DR not found or deleted → treat as terminal
	}
	for _, terminal := range TerminalDRStatuses {
		if statuses[0] == terminal {
			return true
		}
	}
	return false
}

// RecoverActiveSyncs starts sync goroutines for all approved DRs not yet in terminal states.
// Called once at service startup to resume polling for DRs that were being tracked before restart.
func (s *Service) RecoverActiveSyncs() {
	var drs []db.DeliveryRequest
	if err := s.DB.
		Where("aggregate_status NOT IN ?", TerminalDRStatuses).
		Where("approved_at IS NOT NULL").
		Find(&drs).Error; err != nil {
		s.Logger.Errorf("RecoverActiveSyncs: failed to query active DRs: %s", err)
		return
	}
	for _, dr := range drs {
		s.StartDRSync(dr.ID)
	}
	if len(drs) > 0 {
		s.Logger.Infof("recovered %d active DR sync goroutines", len(drs))
	}
}
