// SigLIP2 Prometheus metrics for the Engine.
// Registered on the default prometheus registry and exposed via HTTP handler.

package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// ContentLatency measures end-to-end pipeline latency per content type.
	ContentLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "engine_content_latency_ms",
			Help:    "End-to-end latency per content type and cache level",
			Buckets: []float64{1, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000},
		},
		[]string{"content_type", "cache_level", "tier"},
		// content_type: image|video|sticker|embed
		// cache_level: L1|L2|L3|inference
		// tier: free|pro|enterprise
	)

	// InferenceBatchSize tracks batch sizes sent to the Inference Service.
	InferenceBatchSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "engine_inference_batch_size",
			Help:    "Batch size sent to Inference Service (1 for images, N for video frames)",
			Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 120},
		},
		[]string{"content_type"},
	)

	// CacheHits counts cache hits by level.
	CacheHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "engine_cache_hit_total",
			Help: "Cache hits by level (L1, L2, L3)",
		},
		[]string{"level"},
	)

	// CacheMisses counts cache misses by level.
	CacheMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "engine_cache_miss_total",
			Help: "Cache misses by level (L1, L2, L3)",
		},
		[]string{"level"},
	)

	// ScoreDistribution tracks SigLIP2 score distribution per category.
	// Used as an accuracy proxy: if the distribution shifts unexpectedly,
	// the model may have drifted (prompt drift, ONNX corruption, etc).
	ScoreDistribution = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "engine_inference_score",
			Help:    "Score distribution per category (drift detection proxy)",
			Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.65, 0.7, 0.75, 0.8, 0.9, 0.95, 1.0},
		},
		[]string{"category"},
	)

	// Decisions counts final moderation decisions by type.
	Decisions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "engine_decision_total",
			Help: "Decisions by action, content type, and tier",
		},
		[]string{"action", "content_type", "tier"},
		// action: allow|block|flag|allow_partial|error_fallback
	)

	// CircuitBreakerStateChanges tracks circuit breaker transitions.
	CircuitBreakerStateChanges = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "engine_circuit_breaker_state_changes_total",
			Help: "Circuit breaker state transitions",
		},
		[]string{"from", "to"},
	)

	// VideoFrames tracks frame counts for video processing.
	VideoFrames = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "engine_video_frames",
			Help:    "Frame statistics for video processing",
			Buckets: []float64{1, 4, 8, 16, 30, 60, 120, 300},
		},
		[]string{"stat"},
		// stat: extracted|cache_hit|cache_miss|sent_to_inference
	)

	// TriggeredRate counts when each category triggers (crosses threshold).
	// Used to detect systematic false positives.
	TriggeredRate = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "engine_triggered_total",
			Help: "Times each category triggered (crossed threshold)",
		},
		[]string{"category"},
	)
)

// Register registers all inference metrics with the default prometheus registry.
// Safe to call multiple times — subsequent calls are no-ops.
func Register() {
	prometheus.MustRegister(
		ContentLatency,
		InferenceBatchSize,
		CacheHits,
		CacheMisses,
		ScoreDistribution,
		Decisions,
		CircuitBreakerStateChanges,
		VideoFrames,
		TriggeredRate,
	)
}
