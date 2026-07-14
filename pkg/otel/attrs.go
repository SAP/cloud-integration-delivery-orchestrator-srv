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
		attribute.Int("dr_id", int(drID)),
		attribute.Int("target_node_id", int(targetNodeID)),
		attribute.String("tenant", tenantName),
		attribute.Int("tr_count", len(trs)),
		attribute.IntSlice("tr_numbers", uintsToInts(trs)),
		attribute.StringSlice("artifacts", artifactStrings(artifacts)),
	}
}

// DeploySpanAttrs builds trace span attributes for a batch deploy operation.
func DeploySpanAttrs(drID uint, tenantID uint, tenantName string, artifacts []ArtifactInfo) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int("dr_id", int(drID)),
		attribute.Int("tenant_id", int(tenantID)),
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
		attribute.IntSlice("tr_numbers", uintsToInts(trs)),
	}
}

// GenerateTRSpanAttrs builds trace span attributes for transport request generation.
func GenerateTRSpanAttrs(drID uint, tenantID uint, tenantName string, opCount int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int("dr_id", int(drID)),
		attribute.Int("tenant_id", int(tenantID)),
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

func uintsToInts(vals []uint) []int {
	out := make([]int, 0, len(vals))
	for _, v := range vals {
		out = append(out, int(v))
	}
	return out
}
