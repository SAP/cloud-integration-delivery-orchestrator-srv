package otel

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ArtifactInfo is a minimal struct to avoid importing db package (circular dependency).
type ArtifactInfo struct {
	TechID  string
	Version string
}

// ImportSpanAttrs builds trace span attributes for a batch import operation.
func ImportSpanAttrs(drID uint, targetNodeID uint, tenantName string, trs []uint, artifacts []ArtifactInfo) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int64("dr_id", int64(drID)),
		attribute.Int64("target_node_id", int64(targetNodeID)),
		attribute.String("tenant", tenantName),
		attribute.Int("tr_count", len(trs)),
		attribute.Int64Slice("tr_numbers", uintsToInt64s(trs)),
		attribute.StringSlice("artifacts", artifactStrings(artifacts)),
	}
}

// DeploySpanAttrs builds trace span attributes for a batch deploy operation.
func DeploySpanAttrs(drID uint, tenantID uint, tenantName string, artifacts []ArtifactInfo) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int64("dr_id", int64(drID)),
		attribute.Int64("tenant_id", int64(tenantID)),
		attribute.String("tenant", tenantName),
		attribute.Int("op_count", len(artifacts)),
		attribute.StringSlice("artifacts", artifactStrings(artifacts)),
	}
}

// MetricAttrs builds metric attributes for import/deploy counters and histograms.
func MetricAttrs(result string, tenantName string) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("result", result),
		attribute.String("tenant", tenantName),
	)
}

// ImportTRAttrs returns attributes for TR numbers (set after pre-checks complete).
func ImportTRAttrs(trs []uint) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int("tr_count", len(trs)),
		attribute.Int64Slice("tr_numbers", uintsToInt64s(trs)),
	}
}

// GenerateTRSpanAttrs builds trace span attributes for transport request generation.
func GenerateTRSpanAttrs(drID uint, tenantID uint, tenantName string, opCount int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int64("dr_id", int64(drID)),
		attribute.Int64("tenant_id", int64(tenantID)),
		attribute.String("tenant", tenantName),
		attribute.Int("op_count", opCount),
	}
}

func artifactStrings(arts []ArtifactInfo) []string {
	out := make([]string, 0, len(arts))
	for _, a := range arts {
		out = append(out, fmt.Sprintf("%s@%s", a.TechID, a.Version))
	}
	return out
}

func uintsToInt64s(vals []uint) []int64 {
	out := make([]int64, 0, len(vals))
	for _, v := range vals {
		out = append(out, int64(v))
	}
	return out
}
