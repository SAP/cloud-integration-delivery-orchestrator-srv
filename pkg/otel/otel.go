package otel

import (
	"context"
	"crypto/tls"

	"github.com/cloudfoundry-community/go-cfenv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"
)

// Init initializes OpenTelemetry tracing if a cloud-logging service binding
// exists in VCAP_SERVICES. If not bound, returns a no-op shutdown function
// and the global TracerProvider remains the default (noop — zero overhead).
func Init(appEnv *cfenv.App, serviceName string, logger *zap.SugaredLogger) func() {
	noop := func() {}

	if appEnv == nil {
		return noop
	}

	binding, err := findCLSBinding(appEnv)
	if err != nil || binding == nil {
		logger.Infof("[otel] cloud-logging service not bound, tracing disabled")
		return noop
	}

	tlsCert, err := tls.X509KeyPair([]byte(binding.cert), []byte(binding.key))
	if err != nil {
		logger.Errorf("[otel] failed to parse cloud-logging mTLS credentials: %s", err)
		return noop
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}

	ctx := context.Background()
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(binding.endpoint),
		otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)),
	)
	if err != nil {
		logger.Errorf("[otel] failed to create OTLP exporter: %s", err)
		return noop
	}

	res, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	logger.Infof("[otel] tracing enabled, exporting to %s", binding.endpoint)

	return func() {
		_ = tp.Shutdown(context.Background())
	}
}

// Tracer returns a named tracer from the global provider.
// When OTel is not initialized, this returns a noop tracer.
func Tracer() oteltrace.Tracer {
	return otel.Tracer("cpi-delivery")
}

// clsBinding holds the OTLP ingestion credentials from a cloud-logging service binding.
type clsBinding struct {
	endpoint string
	cert     string
	key      string
}

// findCLSBinding extracts cloud-logging OTLP credentials from VCAP_SERVICES.
func findCLSBinding(appEnv *cfenv.App) (*clsBinding, error) {
	services, err := appEnv.Services.WithLabel("cloud-logging")
	if err != nil || len(services) == 0 {
		return nil, err
	}

	svc := services[0]
	endpoint, _ := svc.CredentialString("ingest-otlp-endpoint")
	cert, _ := svc.CredentialString("ingest-otlp-cert")
	key, _ := svc.CredentialString("ingest-otlp-key")

	if endpoint == "" || cert == "" || key == "" {
		return nil, nil
	}

	return &clsBinding{
		endpoint: endpoint,
		cert:     cert,
		key:      key,
	}, nil
}
