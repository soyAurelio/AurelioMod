package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// EngineMetrics holds counters and histograms for Engine observability.
// All instruments are created via the global OTel meter provider.
type EngineMetrics struct {
	analysisTotal    metric.Int64Counter
	analysisDuration metric.Float64Histogram
	cacheHits        metric.Int64Counter
	cacheMisses      metric.Int64Counter
	inferenceErrors  metric.Int64Counter
}

// NewEngineMetrics creates metric instruments from the global meter.
// Falls back to noop instruments if OTel is not configured.
func NewEngineMetrics() (*EngineMetrics, error) {
	meter := otel.Meter("engine")

	analysisTotal, err := meter.Int64Counter("engine_analysis_total",
		metric.WithDescription("Total number of content analyses"),
	)
	if err != nil {
		return nil, err
	}

	analysisDuration, err := meter.Float64Histogram("engine_analysis_duration_seconds",
		metric.WithDescription("Analysis processing duration in seconds"),
	)
	if err != nil {
		return nil, err
	}

	cacheHits, err := meter.Int64Counter("cache_hits_total",
		metric.WithDescription("Cache hits by level (l1, l2, l3)"),
	)
	if err != nil {
		return nil, err
	}

	cacheMisses, err := meter.Int64Counter("cache_misses_total",
		metric.WithDescription("Cache misses by level (l1, l2, l3)"),
	)
	if err != nil {
		return nil, err
	}

	inferenceErrors, err := meter.Int64Counter("inference_errors_total",
		metric.WithDescription("AI inference error count"),
	)
	if err != nil {
		return nil, err
	}

	return &EngineMetrics{
		analysisTotal:    analysisTotal,
		analysisDuration: analysisDuration,
		cacheHits:        cacheHits,
		cacheMisses:      cacheMisses,
		inferenceErrors:  inferenceErrors,
	}, nil
}

// RecordAnalysis records a completed analysis with timing.
func (m *EngineMetrics) RecordAnalysis(ctx context.Context, duration time.Duration) {
	m.analysisTotal.Add(ctx, 1)
	m.analysisDuration.Record(ctx, duration.Seconds())
}

// RecordCacheHit records a cache hit at the given level.
func (m *EngineMetrics) RecordCacheHit(ctx context.Context, level string) {
	m.cacheHits.Add(ctx, 1, metric.WithAttributes(attribute.String("level", level)))
}

// RecordCacheMiss records a cache miss at the given level.
func (m *EngineMetrics) RecordCacheMiss(ctx context.Context, level string) {
	m.cacheMisses.Add(ctx, 1, metric.WithAttributes(attribute.String("level", level)))
}

// RecordInferenceError records an AI inference error.
func (m *EngineMetrics) RecordInferenceError(ctx context.Context) {
	m.inferenceErrors.Add(ctx, 1)
}
