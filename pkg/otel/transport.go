package otel

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// WrapTransport wraps an http.RoundTripper with OpenTelemetry instrumentation.
// Every outbound HTTP request gets a child span with method, URL, and status code.
// W3C traceparent header is automatically injected for downstream propagation.
// If base is nil, http.DefaultTransport is used.
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return otelhttp.NewTransport(base)
}
