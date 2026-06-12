// Package telemetry initializes OpenTelemetry tracing and metrics for the
// Engine service. Traces are exported via OTLP HTTP to Grafana Tempo;
// metrics are exported via OTLP HTTP to VictoriaMetrics.
//
// When OTEL_EXPORTER_OTLP_ENDPOINT is empty (or not set), noop providers
// are returned — suitable for development and CI without a collector.
//
// Spans are created at pipeline stage boundaries:
//   - pipeline.analyze (root span)
//   - cache.l1_check, cache.l2_check, cache.l3_check
//   - wavespeed.analyze
//
// Metrics:
//   - cache_hits_total (by cache_level: l1, l2, l3)
//   - cache_misses_total (by cache_level: l1, l2, l3)
//   - analysis_duration_seconds (histogram)
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Telemetry holds the initialized OpenTelemetry providers.
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
}

// Config holds telemetry initialization configuration.
type Config struct {
	// OTLPEndpoint is the OTLP HTTP collector endpoint (e.g., "localhost:4317"
	// for Tempo). If empty, falls back to the OTEL_EXPORTER_OTLP_ENDPOINT env
	// var. If both are empty, returns noop providers.
	OTLPEndpoint string

	// ServiceName is the OpenTelemetry service.name resource attribute.
	// Default: "engine" if empty.
	ServiceName string
}

// Init initializes OpenTelemetry tracing and metrics providers.
//
// When OTLPEndpoint is empty (and the env var is unset), noop providers
// are returned. This is the default for development and CI.
//
// Graceful shutdown of the returned providers should be done via
// Telemetry.Shutdown before the process exits.
func Init(ctx context.Context, cfg Config) (*Telemetry, error) {
	endpoint := cfg.OTLPEndpoint
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		return initNoop()
	}

	// HTTP exporter uses full URL. Pass endpoint as-is from config.
	// Grafana Cloud: "https://hostname/otlp" — exporter appends /v1/traces or /v1/metrics.
	// Local Tempo: "http://tempo:4317" — works with HTTP exporter too.

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "engine"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("0.1.0"),
			attribute.String("deployment.environment", "production"),
		),
		resource.WithFromEnv(), // OTEL_RESOURCE_ATTRIBUTES
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	// Initialize tracer provider
	tp, err := initTracerProvider(ctx, endpoint, res)
	if err != nil {
		return nil, fmt.Errorf("telemetry tracer: %w", err)
	}

	// Initialize meter provider
	mp, err := initMeterProvider(ctx, endpoint, res)
	if err != nil {
		// Clean up tracer provider on partial failure
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(shutdownCtx)
		return nil, fmt.Errorf("telemetry meter: %w", err)
	}

	// Set global propagator for trace context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	slog.Info("telemetry initialized",
		"endpoint", endpoint,
		"service", serviceName,
	)

	return &Telemetry{
		tracerProvider: tp,
		meterProvider:  mp,
	}, nil
}

// initNoop returns a Telemetry with noop providers. Used when no OTLP
// collector is configured.
func initNoop() (*Telemetry, error) {
	slog.Debug("telemetry: using noop providers (no OTLP endpoint configured)")
	return &Telemetry{
		tracerProvider: sdktrace.NewTracerProvider(),
		meterProvider:  sdkmetric.NewMeterProvider(),
	}, nil
}

// initTracerProvider creates an OTLP HTTP trace exporter and configures
// the global tracer provider with batch export.
//
// TLS: enabled by default (required for Grafana Cloud). Set OTEL_INSECURE=true
// for local dev (localhost Tempo without TLS).
// Auth: reads OTEL_EXPORTER_OTLP_HEADERS env var (Grafana Cloud Basic auth).
func initTracerProvider(ctx context.Context, _ string, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	// Endpoint is read from OTEL_EXPORTER_OTLP_ENDPOINT env var by the SDK.
	opts := []otlptracehttp.Option{}
	if os.Getenv("OTEL_INSECURE") == "true" {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	// Auth headers (Grafana Cloud: Authorization=Basic <base64>)
	if h := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); h != "" {
		headers := parseOTLPHeaders(h)
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	return tp, nil
}

// initMeterProvider creates an OTLP HTTP metric exporter and configures
// the global meter provider with periodic export to VictoriaMetrics/Grafana Cloud.
//
// TLS: enabled by default. Auth: reads OTEL_EXPORTER_OTLP_HEADERS env var.
func initMeterProvider(ctx context.Context, _ string, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	// Endpoint is read from OTEL_EXPORTER_OTLP_ENDPOINT env var by the SDK.
	opts := []otlpmetrichttp.Option{}
	if os.Getenv("OTEL_INSECURE") == "true" {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	// Auth headers (Grafana Cloud: Authorization=Basic <base64>)
	if h := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); h != "" {
		headers := parseOTLPHeaders(h)
		opts = append(opts, otlpmetrichttp.WithHeaders(headers))
	}
	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(exporter,
				sdkmetric.WithInterval(15*time.Second),
			),
		),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(mp)
	return mp, nil
}

// parseOTLPHeaders parses the OTEL_EXPORTER_OTLP_HEADERS env var (key=value
// pairs separated by commas) into a map for gRPC metadata.
// Example: "Authorization=Basic abc123,Other=value" → map[string]string{...}
func parseOTLPHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) == 2 {
			headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return headers
}

// Shutdown gracefully shuts down both the tracer and meter providers,
// flushing any pending spans and metrics. Safe to call multiple times —
// subsequent calls are a no-op.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var firstErr error

	if t.tracerProvider != nil {
		if err := t.tracerProvider.Shutdown(shutdownCtx); err != nil {
			slog.Error("tracer shutdown failed", "error", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			slog.Debug("tracer provider shut down")
		}
		// Prevent double-shutdown by nil-ing the provider
		t.tracerProvider = nil
	}

	if t.meterProvider != nil {
		if err := t.meterProvider.Shutdown(shutdownCtx); err != nil {
			slog.Error("meter shutdown failed", "error", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			slog.Debug("meter provider shut down")
		}
		// Prevent double-shutdown by nil-ing the provider
		t.meterProvider = nil
	}

	return firstErr
}
