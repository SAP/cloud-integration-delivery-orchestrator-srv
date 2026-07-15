package otel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/cloudfoundry-community/go-cfenv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
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
		logger.Infof("[otel] not running in CF environment, observability disabled")
		return noop
	}

	binding, err := findCLSBinding(appEnv)
	if err != nil {
		logger.Warnf("[otel] %s — tracing disabled", err)
		return noop
	}
	if binding == nil {
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

	if binding.serverCA != "" {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM([]byte(binding.serverCA)) {
			tlsConfig.RootCAs = pool
			logger.Infof("[otel] server-ca loaded for TLS verification")
		} else {
			logger.Warnf("[otel] failed to parse server-ca, falling back to system roots")
		}
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

	// --- Metrics ---
	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(binding.endpoint),
		otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)),
	)
	if err != nil {
		logger.Warnf("[otel] failed to create metric exporter (traces still active): %s", err)
		return func() { _ = tp.Shutdown(context.Background()) }
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(60*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	logger.Infof("[otel] initialized: service=%s, endpoint=%s, traces=enabled, metrics=enabled (interval=60s)", serviceName, binding.endpoint)

	return func() {
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
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
	serverCA string
}

// findCLSBinding extracts cloud-logging mTLS credentials from VCAP_SERVICES.
func findCLSBinding(appEnv *cfenv.App) (*clsBinding, error) {
	services, err := appEnv.Services.WithLabel("cloud-logging")
	if err != nil || len(services) == 0 {
		return nil, err
	}

	svc := services[0]
	endpoint, _ := svc.CredentialString("ingest-mtls-endpoint")
	cert, _ := svc.CredentialString("ingest-mtls-cert")
	key, _ := svc.CredentialString("ingest-mtls-key")
	serverCA, _ := svc.CredentialString("server-ca")

	if endpoint == "" || cert == "" || key == "" {
		return nil, fmt.Errorf("cloud-logging service bound but missing mTLS credentials (endpoint=%q, cert=%v, key=%v)",
			endpoint, cert != "", key != "")
	}

	// gRPC requires host:port — append default HTTPS port if not present
	if !strings.Contains(endpoint, ":") {
		endpoint = endpoint + ":443"
	}

	return &clsBinding{
		endpoint: endpoint,
		cert:     cert,
		key:      key,
		serverCA: serverCA,
	}, nil
}
