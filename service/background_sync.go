package service

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"mmt-delivery/db"
	"mmt-delivery/pkg/lifecycle"
)

var ActiveDRStatuses = []lifecycle.AggregateStatus{
	lifecycle.AggAwaitingImport,
	lifecycle.AggImporting,
	lifecycle.AggImportFailed,
	lifecycle.AggWaitingDeploy,
	lifecycle.AggDeploying,
	lifecycle.AggDeployFailed,
}

type opSnapshot struct {
	ID          uint                  `json:"opId"`
	ImportState lifecycle.ImportState `json:"import"`
	DeployState lifecycle.DeployState `json:"deploy"`
}

type drSnapshot struct {
	Exists bool
	Status lifecycle.AggregateStatus
	Ops    []opSnapshot
}

func (s *Service) StartBackgroundSync(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.syncActiveDRs(ctx)
			}
		}
	}()
}

func (s *Service) syncActiveDRs(ctx context.Context) {
	var drs []db.DeliveryRequest
	if err := s.DB.
		Where("aggregate_status IN ?", ActiveDRStatuses).
		Find(&drs).Error; err != nil {
		s.Logger.Errorf("background sync: failed to query active delivery requests: %s", err)
		return
	}

	for _, dr := range drs {
		if err := s.SyncDeliveryStatus(dr.ID, "system"); err != nil {
			s.Logger.Warnf("background sync: failed syncing dr #%d: %s", dr.ID, err)
		}
	}
}

func (s *Service) captureDrSnapshot(drID uint) drSnapshot {
	var dr db.DeliveryRequest
	if err := s.DB.First(&dr, drID).Error; err != nil {
		return drSnapshot{Exists: false}
	}
	return drSnapshot{
		Exists: true,
		Status: dr.AggregateStatus,
		Ops:    s.snapshotOps(drID),
	}
}

func (s *Service) snapshotOps(drID uint) []opSnapshot {
	var ops []db.ArtifactTenantOperation
	if err := s.DB.
		Where("delivery_request_id = ?", drID).
		Order("id ASC").
		Find(&ops).Error; err != nil {
		return nil
	}

	snapshots := make([]opSnapshot, 0, len(ops))
	for _, op := range ops {
		snapshots = append(snapshots, opSnapshot{
			ID:          op.ID,
			ImportState: op.ImportState,
			DeployState: op.DeployState,
		})
	}
	return snapshots
}

func sameOps(a, b []opSnapshot) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}

	aCopy := append([]opSnapshot(nil), a...)
	bCopy := append([]opSnapshot(nil), b...)
	sort.Slice(aCopy, func(i, j int) bool { return aCopy[i].ID < aCopy[j].ID })
	sort.Slice(bCopy, func(i, j int) bool { return bCopy[i].ID < bCopy[j].ID })

	for i := range aCopy {
		if aCopy[i].ID != bCopy[i].ID ||
			aCopy[i].ImportState != bCopy[i].ImportState ||
			aCopy[i].DeployState != bCopy[i].DeployState {
			return false
		}
	}
	return true
}

func (s *Service) publishDrOps(drID uint, ops []opSnapshot) {
	if s.EventBus == nil {
		return
	}
	payload := struct {
		DrID uint         `json:"drId"`
		Ops  []opSnapshot `json:"ops"`
	}{
		DrID: drID,
		Ops:  ops,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.EventBus.Publish(Event{Type: EventDrOps, Payload: data})
}

func (s *Service) publishDrStatus(drID uint, oldStatus, newStatus lifecycle.AggregateStatus) {
	if s.EventBus == nil {
		return
	}
	payload := map[string]any{
		"drId":      drID,
		"oldStatus": oldStatus,
		"newStatus": newStatus,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.EventBus.Publish(Event{Type: EventDrStatus, Payload: data})
}

func (s *Service) publishCounts() {
	if s.EventBus == nil {
		return
	}

	var total int64
	if err := s.DB.Model(&db.DeliveryRequest{}).Count(&total).Error; err != nil {
		return
	}
	var counts []struct {
		AggregateStatus lifecycle.AggregateStatus
		Count           uint
	}
	if err := s.DB.Model(&db.DeliveryRequest{}).
		Select("aggregate_status, count(*) as count").
		Group("aggregate_status").
		Scan(&counts).Error; err != nil {
		return
	}

	statusCounts := make(map[string]uint, len(counts))
	for _, c := range counts {
		statusCounts[string(c.AggregateStatus)] = c.Count
	}
	data, err := json.Marshal(map[string]any{
		"total":        total,
		"statusCounts": statusCounts,
	})
	if err != nil {
		return
	}
	s.EventBus.Publish(Event{Type: EventCounts, Payload: data})
}
