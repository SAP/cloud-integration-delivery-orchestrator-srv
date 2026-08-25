package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Meter returns the global meter for this application.
// When OTel is not initialized, returns a noop meter (zero overhead).
func meter() metric.Meter {
	return otel.Meter("cpi-delivery")
}

// Pre-registered instruments. Safe to use even when OTel is noop — all
// operations become no-ops without allocation.
var (
	ImportTotal, _ = meter().Int64Counter("cpi_delivery.import.ops",
		metric.WithDescription("Number of artifact operations imported by result"))
	DeployTotal, _ = meter().Int64Counter("cpi_delivery.deploy.ops",
		metric.WithDescription("Number of artifact operations deployed by result"))
	ImportDuration, _ = meter().Float64Histogram("cpi_delivery.import.duration_seconds",
		metric.WithDescription("Import operation end-to-end duration"))
	DeployDuration, _ = meter().Float64Histogram("cpi_delivery.deploy.duration_seconds",
		metric.WithDescription("Deploy operation end-to-end duration"))

	// Git Sync metrics
	GitSyncTotal, _ = meter().Int64Counter("cpi_delivery.git_sync.ops",
		metric.WithDescription("Number of git sync operations by result (completed/failed/skipped)"))
	GitSyncDuration, _ = meter().Float64Histogram("cpi_delivery.git_sync.duration_seconds",
		metric.WithDescription("Git sync operation duration"))
)

// RegisterSyncTrackerGauge registers an observable gauge that reports the
// number of active SyncTracker goroutines. Called once at startup.
func RegisterSyncTrackerGauge(countFn func() int64) {
	gauge, err := meter().Int64ObservableGauge("cpi_delivery.sync_tracker.active",
		metric.WithDescription("Number of active SyncTracker goroutines"),
	)
	if err != nil {
		return
	}
	_, _ = meter().RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(gauge, countFn())
		return nil
	}, gauge)
}
