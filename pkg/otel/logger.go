package otel

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

// TraceFields extracts trace_id and span_id from the context and returns them
// as key-value pairs suitable for zap's Infow/Errorw structured logging.
// Returns nil if no active span exists (safe to append to any slice).
func TraceFields(ctx context.Context) []interface{} {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return nil
	}
	return []interface{}{
		"trace_id", span.SpanContext().TraceID().String(),
		"span_id", span.SpanContext().SpanID().String(),
	}
}
