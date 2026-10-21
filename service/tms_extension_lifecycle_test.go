package service

import (
	"strings"
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/db"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/lifecycle"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestTmsTransitions_AllValidEdges verifies every edge in the allowedTmsTransitions table.
func TestTmsTransitions_AllValidEdges(t *testing.T) {
	tests := []struct {
		name      string
		from      lifecycle.TmsExtensionState
		event     TmsEvent
		wantState lifecycle.TmsExtensionState
	}{
		// not_started
		{"not_started → node_selected → node_pending", lifecycle.TmsNotStarted, TmsEventNodeSelected, lifecycle.TmsNodePending},
		{"not_started → reset → not_started", lifecycle.TmsNotStarted, TmsEventReset, lifecycle.TmsNotStarted},
		// node_pending
		{"node_pending → node_selected → node_pending", lifecycle.TmsNodePending, TmsEventNodeSelected, lifecycle.TmsNodePending},
		{"node_pending → route_confirmed → node_confirmed", lifecycle.TmsNodePending, TmsEventRouteConfirmed, lifecycle.TmsNodeConfirmed},
		{"node_pending → reset → not_started", lifecycle.TmsNodePending, TmsEventReset, lifecycle.TmsNotStarted},
		// node_confirmed
		{"node_confirmed → node_selected → node_pending", lifecycle.TmsNodeConfirmed, TmsEventNodeSelected, lifecycle.TmsNodePending},
		{"node_confirmed → apply_started → bootstrapping", lifecycle.TmsNodeConfirmed, TmsEventApplyStarted, lifecycle.TmsBootstrapping},
		{"node_confirmed → reset → not_started", lifecycle.TmsNodeConfirmed, TmsEventReset, lifecycle.TmsNotStarted},
		// bootstrapping
		{"bootstrapping → apply_finished → bootstrapped", lifecycle.TmsBootstrapping, TmsEventApplyFinished, lifecycle.TmsBootstrapped},
		{"bootstrapping → apply_failed → bootstrap_failed", lifecycle.TmsBootstrapping, TmsEventApplyFailed, lifecycle.TmsBootstrapFailed},
		// bootstrapped
		{"bootstrapped → node_selected → node_pending", lifecycle.TmsBootstrapped, TmsEventNodeSelected, lifecycle.TmsNodePending},
		{"bootstrapped → apply_started → bootstrapping", lifecycle.TmsBootstrapped, TmsEventApplyStarted, lifecycle.TmsBootstrapping},
		{"bootstrapped → invalidated → bootstrap_failed", lifecycle.TmsBootstrapped, TmsEventInvalidated, lifecycle.TmsBootstrapFailed},
		{"bootstrapped → reset → not_started", lifecycle.TmsBootstrapped, TmsEventReset, lifecycle.TmsNotStarted},
		// bootstrap_failed
		{"bootstrap_failed → node_selected → node_pending", lifecycle.TmsBootstrapFailed, TmsEventNodeSelected, lifecycle.TmsNodePending},
		{"bootstrap_failed → apply_started → bootstrapping", lifecycle.TmsBootstrapFailed, TmsEventApplyStarted, lifecycle.TmsBootstrapping},
		{"bootstrap_failed → reset → not_started", lifecycle.TmsBootstrapFailed, TmsEventReset, lifecycle.TmsNotStarted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, database := newTestSvc(t)
			tenant := db.CpiTenant{
				Name:           tt.name,
				CfApiEndpoint:  "https://test",
				CfOrg:          tt.name,
				LifecycleState: lifecycle.TenantReady,
				TmsExtensionState: tt.from,
			}
			if err := database.Create(&tenant).Error; err != nil {
				t.Fatalf("create: %v", err)
			}

			if err := svc.TransitionTms(tenant.ID, tt.event); err != nil {
				t.Fatalf("TransitionTms(%q, %q): %v", tt.from, tt.event, err)
			}

			var got db.CpiTenant
			if err := database.First(&got, tenant.ID).Error; err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.TmsExtensionState != tt.wantState {
				t.Errorf("TmsExtensionState = %q, want %q", got.TmsExtensionState, tt.wantState)
			}
		})
	}
}

// TestTmsTransitions_InvalidEdgesRejected verifies that transitions not in the
// table are rejected with ErrTmsTransitionNotAllowed.
func TestTmsTransitions_InvalidEdgesRejected(t *testing.T) {
	tests := []struct {
		name  string
		from  lifecycle.TmsExtensionState
		event TmsEvent
	}{
		// not_started: cannot confirm, apply, finish, fail, or invalidate
		{"not_started × route_confirmed", lifecycle.TmsNotStarted, TmsEventRouteConfirmed},
		{"not_started × apply_started", lifecycle.TmsNotStarted, TmsEventApplyStarted},
		{"not_started × apply_finished", lifecycle.TmsNotStarted, TmsEventApplyFinished},
		{"not_started × invalidated", lifecycle.TmsNotStarted, TmsEventInvalidated},
		// node_pending: cannot apply, finish, fail, or invalidate
		{"node_pending × apply_started", lifecycle.TmsNodePending, TmsEventApplyStarted},
		{"node_pending × apply_finished", lifecycle.TmsNodePending, TmsEventApplyFinished},
		{"node_pending × invalidated", lifecycle.TmsNodePending, TmsEventInvalidated},
		// node_confirmed: cannot confirm, finish, fail, or invalidate
		{"node_confirmed × route_confirmed", lifecycle.TmsNodeConfirmed, TmsEventRouteConfirmed},
		{"node_confirmed × apply_finished", lifecycle.TmsNodeConfirmed, TmsEventApplyFinished},
		{"node_confirmed × invalidated", lifecycle.TmsNodeConfirmed, TmsEventInvalidated},
		// bootstrapping: cannot select node, confirm, apply, invalidate, or reset
		{"bootstrapping × node_selected", lifecycle.TmsBootstrapping, TmsEventNodeSelected},
		{"bootstrapping × route_confirmed", lifecycle.TmsBootstrapping, TmsEventRouteConfirmed},
		{"bootstrapping × apply_started", lifecycle.TmsBootstrapping, TmsEventApplyStarted},
		{"bootstrapping × invalidated", lifecycle.TmsBootstrapping, TmsEventInvalidated},
		{"bootstrapping × reset", lifecycle.TmsBootstrapping, TmsEventReset},
		// bootstrapped: cannot confirm or finish
		{"bootstrapped × route_confirmed", lifecycle.TmsBootstrapped, TmsEventRouteConfirmed},
		{"bootstrapped × apply_finished", lifecycle.TmsBootstrapped, TmsEventApplyFinished},
		// bootstrap_failed: cannot confirm, finish, or invalidate
		{"bootstrap_failed × route_confirmed", lifecycle.TmsBootstrapFailed, TmsEventRouteConfirmed},
		{"bootstrap_failed × apply_finished", lifecycle.TmsBootstrapFailed, TmsEventApplyFinished},
		{"bootstrap_failed × invalidated", lifecycle.TmsBootstrapFailed, TmsEventInvalidated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, database := newTestSvc(t)
			tenant := db.CpiTenant{
				Name:           tt.name,
				CfApiEndpoint:  "https://test",
				CfOrg:          tt.name,
				LifecycleState: lifecycle.TenantReady,
				TmsExtensionState: tt.from,
			}
			if err := database.Create(&tenant).Error; err != nil {
				t.Fatalf("create: %v", err)
			}

			err := svc.TransitionTms(tenant.ID, tt.event)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errorContains(err, ErrTmsTransitionNotAllowed.Error()) {
				t.Errorf("error = %v, want containing %q", err, ErrTmsTransitionNotAllowed.Error())
			}

			// State must be unchanged.
			var got db.CpiTenant
			if err := database.First(&got, tenant.ID).Error; err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.TmsExtensionState != tt.from {
				t.Errorf("TmsExtensionState changed to %q, want unchanged %q", got.TmsExtensionState, tt.from)
			}
		})
	}
}

// TestTmsTransitions_BootstrappingBlocksReset verifies that bootstrapping state blocks reset
// (rule 1 cannot fire while TMS bootstrap is running).
func TestTmsTransitions_BootstrappingBlocksReset(t *testing.T) {
	svc, database := newTestSvc(t)
	tenant := db.CpiTenant{
		Name:              "bootstrapping-blocks-reset",
		CfApiEndpoint:     "https://test",
		CfOrg:             "bootstrapping-reset",
		LifecycleState:    lifecycle.TenantReady,
		TmsExtensionState: lifecycle.TmsBootstrapping,
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	err := svc.TransitionTms(tenant.ID, TmsEventReset)
	if err == nil {
		t.Fatal("expected error for reset from bootstrapping, got nil")
	}
}

// newTestSvc creates a Service with a per-test in-memory SQLite DB.
func newTestSvc(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	database := newTestDB(t)
	svc := &Service{DB: database}
	return svc, database
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=private"
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.AutoMigrate(&db.CpiTenant{}, &db.TenantBootstrapJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func errorContains(err error, substr string) bool {
	return err != nil && strings.Contains(err.Error(), substr)
}
