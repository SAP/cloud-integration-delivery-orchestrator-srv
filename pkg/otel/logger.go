package otel

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
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

// WithTrace returns a child logger that automatically includes trace_id and
// span_id from the context. If ctx has no active span, the original logger
// is returned unchanged (zero allocation).
func WithTrace(ctx context.Context, logger *zap.SugaredLogger) *zap.SugaredLogger {
	fields := TraceFields(ctx)
	if len(fields) == 0 {
		return logger
	}
	return logger.With(fields...)
}
