// Package pipeline orchestrates content analysis through the L1→L2→L3→WaveSpeed
// cache hierarchy. Each layer is progressively more expensive but covers more
// types of matches:
//
//	L1: BLAKE3 exact match (<5ms)     → same content was analyzed before
//	L2: pHash perceptual match (<50ms) → visually similar content found
//	L3: Weaviate vector search (<200ms) → semantically similar content found
//	WaveSpeed: AI API (seconds)        → fresh analysis (fallback)
//
// On cache miss, the pipeline calls WaveSpeed and back-populates all cache layers.
// After a WaveSpeed decision, integration hooks fire: audit emission, NATS
// publish, and quarantine update — all fire-and-forget (non-blocking).
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/soyAurelio/AurelioMod/engine/analyzer"
	"github.com/soyAurelio/AurelioMod/engine/hasher"
	"github.com/soyAurelio/AurelioMod/internal/cache"
	"github.com/soyAurelio/AurelioMod/internal/weaviate"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// ContentNormalizer normalizes raw content bytes into decoded RGB24 pixel data.
// hasher.Normalizer satisfies this interface.
type ContentNormalizer interface {
	Normalize(ctx context.Context, input []byte) (*hasher.NormalizeResult, error)
}

// Pipeline orchestrates content analysis through cache layers.
type Pipeline interface {
	// Execute runs the full analysis pipeline on an AnalyzeRequest.
	// Returns an AnalyzeResponse with the cache level that produced the decision,
	// or CACHE_LEVEL_NONE if WaveSpeed performed a fresh analysis.
	Execute(ctx context.Context, req *v1.AnalyzeRequest) (*v1.AnalyzeResponse, error)
}

// AuditHook is called after a WaveSpeed decision is produced to emit an
// immutable audit event. Implementations write to slog, Neon DB, and R2.
type AuditHook func(ctx context.Context, workspaceID, contentHash, decision, category string, confidence float64, processingMs int64)

// DecisionHook is called after a WaveSpeed decision to publish the result
// via NATS for Centrifugo → dashboard real-time relay.
type DecisionHook func(ctx context.Context, workspaceID, contentHash, decision, category string, confidence float64)

// QuarantineHook is called after a WaveSpeed decision to update the
// inverted quarantine state machine (PENDING → BLOCKED | RELEASED).
type QuarantineHook func(ctx context.Context, contentID, decision, category string, confidence float64)

// PipelineOption configures optional pipeline behavior.
type PipelineOption func(*pipeline)

// WithAuditHook sets the audit emission hook, fired after WaveSpeed decisions.
func WithAuditHook(h AuditHook) PipelineOption {
	return func(p *pipeline) { p.auditHook = h }
}

// WithDecisionHook sets the NATS decision publishing hook.
func WithDecisionHook(h DecisionHook) PipelineOption {
	return func(p *pipeline) { p.decisionHook = h }
}

// WithQuarantineHook sets the quarantine state update hook.
func WithQuarantineHook(h QuarantineHook) PipelineOption {
	return func(p *pipeline) { p.quarantineHook = h }
}

type pipeline struct {
	l1cache        cache.L1Cache
	l2cache        cache.L2Cache
	normalizer     ContentNormalizer
	analyzer       analyzer.Analyzer
	weaviateClient weaviate.WeaviateClient

	// Integration hooks (fire-and-forget, non-blocking)
	auditHook      AuditHook
	decisionHook   DecisionHook
	quarantineHook QuarantineHook

	wg sync.WaitGroup // tracks in-flight hook goroutines
}

// New creates a Pipeline backed by L1/L2 caches, content normalizer,
// WaveSpeed analyzer, and Weaviate L3 vector search client.
//
// analyzer and weaviateClient may be nil — in that case L3+WaveSpeed
// layers are skipped and a QUEUED decision is returned on cache miss.
//
// Optional hooks (audit, decision publish, quarantine) can be configured
// via PipelineOption functions. All hooks are fire-and-forget.
func New(
	l1 cache.L1Cache,
	l2 cache.L2Cache,
	norm ContentNormalizer,
	a analyzer.Analyzer,
	wv weaviate.WeaviateClient,
	opts ...PipelineOption,
) Pipeline {
	p := &pipeline{
		l1cache:        l1,
		l2cache:        l2,
		normalizer:     norm,
		analyzer:       a,
		weaviateClient: wv,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Execute runs the normalization → L1 → L2 → L3 → WaveSpeed cascade.
func (p *pipeline) Execute(ctx context.Context, req *v1.AnalyzeRequest) (*v1.AnalyzeResponse, error) {
	start := time.Now()

	// Step 1: Normalize raw bytes into RGB24 pixel data
	normalized, err := p.normalizer.Normalize(ctx, req.RawBytes)
	if err != nil {
		slog.ErrorContext(ctx, "normalization failed",
			"error", err,
			"workspace_id", req.WorkspaceId,
			"content_id", req.ContentId,
		)
		return nil, fmt.Errorf("pipeline normalize: %w", err)
	}
	pixels := normalized.RGBPixels

	// Step 2: Compute BLAKE3 hash over raw pixels (deterministic, used as L1 key)
	l1Hash := hasher.HashL1(pixels)
	ph := hasher.PHash(pixels)

	// Step 3: Check L1 cache (exact match)
	if d, ok := p.l1cache.GetL1(ctx, l1Hash); ok {
		slog.InfoContext(ctx, "L1 cache hit",
			"hash", l1Hash,
			"category", d.Category,
			"workspace_id", req.WorkspaceId,
		)
		return buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3, l1Hash, time.Since(start)), nil
	}

	// L1 miss
	slog.DebugContext(ctx, "L1 cache miss",
		"blake3", l1Hash,
		"workspace_id", req.WorkspaceId,
	)

	// Step 4: Check L2 cache (perceptual match within Hamming ≤ 5)
	l2Start := time.Now()
	results, err := p.l2cache.GetL2(ctx, ph, hasher.HammingThreshold)
	if err != nil {
		slog.WarnContext(ctx, "L2 cache unavailable, proceeding as miss",
			"error", err,
			"phash", fmt.Sprintf("%016x", ph),
			"workspace_id", req.WorkspaceId,
		)
	} else if len(results) > 0 {
		slog.InfoContext(ctx, "L2 cache hit",
			"phash", fmt.Sprintf("%016x", ph),
			"category", results[0].Category,
			"duration_ms", time.Since(l2Start).Milliseconds(),
			"workspace_id", req.WorkspaceId,
		)
		return buildResponse(results[0], v1.CacheLevel_CACHE_LEVEL_L2_PHASH, l1Hash, time.Since(start)), nil
	}

	slog.DebugContext(ctx, "L2 cache miss",
		"phash", fmt.Sprintf("%016x", ph),
		"duration_ms", time.Since(l2Start).Milliseconds(),
		"workspace_id", req.WorkspaceId,
	)

	// Step 5: Check L3 cache (Weaviate vector search) — if available
	if p.weaviateClient != nil {
		l3Start := time.Now()
		d, err := p.weaviateClient.SearchSimilar(ctx, l1Hash, 0.92)
		if err != nil {
			slog.WarnContext(ctx, "L3 Weaviate unavailable, skipping to WaveSpeed",
				"error", err,
				"workspace_id", req.WorkspaceId,
			)
		} else if d != nil {
			slog.InfoContext(ctx, "L3 cache hit",
				"content_hash", l1Hash,
				"category", d.Category,
				"duration_ms", time.Since(l3Start).Milliseconds(),
				"workspace_id", req.WorkspaceId,
			)
			return buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L3_WEAVIATE, l1Hash, time.Since(start)), nil
		} else {
			slog.DebugContext(ctx, "L3 cache miss",
				"content_hash", l1Hash,
				"duration_ms", time.Since(l3Start).Milliseconds(),
				"workspace_id", req.WorkspaceId,
			)
		}
	}

	// Step 6: WaveSpeed analysis (final fallback) — if available
	if p.analyzer == nil {
		// No analyzer configured — return QUEUED for downstream
		slog.InfoContext(ctx, "cache miss, no analyzer configured",
			"blake3", l1Hash,
			"phash", fmt.Sprintf("%016x", ph),
			"workspace_id", req.WorkspaceId,
		)
		return &v1.AnalyzeResponse{
			Decision:     v1.Decision_DECISION_QUEUED,
			ContentHash:  l1Hash,
			CacheLevel:   v1.CacheLevel_CACHE_LEVEL_NONE,
			ProcessingMs: time.Since(start).Milliseconds(),
		}, nil
	}

	slog.InfoContext(ctx, "all caches missed, calling WaveSpeed",
		"blake3", l1Hash,
		"workspace_id", req.WorkspaceId,
	)

	wsStart := time.Now()
	mimeType := normalized.MIMEType
	if mimeType == "" {
		mimeType = "image/jpeg" // default when MIME detection fails
	}

	// The imageURL is the content hash — we don't have a real URL yet.
	// In production, the Edge bot provides the public URL via a separate field.
	// For now, pass the hash as a placeholder.
	imageURL := fmt.Sprintf("https://storage.aureliomod.dev/%s", l1Hash)

	result, err := p.analyzer.Analyze(ctx, imageURL, mimeType)
	if err != nil {
		slog.ErrorContext(ctx, "WaveSpeed analysis failed",
			"error", err,
			"workspace_id", req.WorkspaceId,
		)
		return nil, fmt.Errorf("pipeline wavespeed: %w", err)
	}

	wsDuration := time.Since(wsStart)
	slog.InfoContext(ctx, "WaveSpeed analysis complete",
		"decision", result.Decision,
		"confidence", result.Confidence,
		"duration_ms", wsDuration.Milliseconds(),
		"workspace_id", req.WorkspaceId,
	)

	// Convert WaveSpeed result to a CachedDecision for back-population
	cached := &cache.CachedDecision{
		Decision:   decisionFromBool(result.Decision),
		Confidence: result.Confidence,
		Category:   dominantCategory(result.Categories),
	}

	// Step 7: Back-populate all cache layers (best-effort, non-blocking)
	p.backPopulate(ctx, l1Hash, ph, cached)

	// Step 8: Fire integration hooks (audit, NATS, quarantine) — fire-and-forget.
	// All hooks run in goroutines so they never block the response pipeline.
	p.fireHooks(ctx, req, l1Hash, cached, time.Since(start).Milliseconds())

	return &v1.AnalyzeResponse{
		Decision:     cached.Decision,
		BlockReason:  cached.Category,
		Confidence:   cached.Confidence,
		Category:     cached.Category,
		ContentHash:  l1Hash,
		CacheLevel:   v1.CacheLevel_CACHE_LEVEL_NONE,
		ProcessingMs: time.Since(start).Milliseconds(),
	}, nil
}

// backPopulate stores the WaveSpeed decision in L1, L2, and L3 caches.
// Failures are logged as warnings — cache population is best-effort per spec.
func (p *pipeline) backPopulate(ctx context.Context, l1Hash string, ph uint64, d *cache.CachedDecision) {
	// L1: BLAKE3 exact match
	if err := p.l1cache.SetL1(ctx, l1Hash, d); err != nil {
		slog.WarnContext(ctx, "L1 back-population failed",
			"error", err,
			"hash", l1Hash,
		)
	}

	// L2: pHash perceptual match
	if err := p.l2cache.SetL2(ctx, ph, d); err != nil {
		slog.WarnContext(ctx, "L2 back-population failed",
			"error", err,
			"phash", fmt.Sprintf("%016x", ph),
		)
	}

	// L3: Weaviate vector index — if client available
	if p.weaviateClient != nil {
		if err := p.weaviateClient.IndexDecision(ctx, l1Hash, d); err != nil {
			slog.WarnContext(ctx, "L3 back-population failed",
				"error", err,
				"content_hash", l1Hash,
			)
		}
	}
}

// decisionFromBool maps a WaveSpeed boolean flag to a protobuf Decision.
func decisionFromBool(flagged bool) v1.Decision {
	if flagged {
		return v1.Decision_DECISION_BLOCK
	}
	return v1.Decision_DECISION_ALLOW
}

// dominantCategory returns the first flagged category from the WaveSpeed output.
// If none are flagged, returns "safe".
func dominantCategory(categories map[string]bool) string {
	// Priority-ordered category list: more severe first
	priority := []string{"sexual/minors", "violence", "harassment", "hate", "sexual"}
	for _, cat := range priority {
		if categories[cat] {
			return cat
		}
	}
	return "safe"
}

// buildResponse converts a CachedDecision + cache level into an AnalyzeResponse.
func buildResponse(d *cache.CachedDecision, level v1.CacheLevel, l1Hash string, elapsed time.Duration) *v1.AnalyzeResponse {
	return &v1.AnalyzeResponse{
		Decision:     d.Decision,
		BlockReason:  d.Category,
		Confidence:   d.Confidence,
		Category:     d.Category,
		ContentHash:  l1Hash,
		CacheLevel:   level,
		ProcessingMs: elapsed.Milliseconds(),
	}
}

// fireHooks launches all configured integration hooks in fire-and-forget
// goroutines. Hooks are non-blocking — they must never delay the pipeline
// response. Failures within hooks are logged by the hook implementations.
func (p *pipeline) fireHooks(ctx context.Context, req *v1.AnalyzeRequest, contentHash string, d *cache.CachedDecision, processingMs int64) {
	decision := d.Decision.String()
	workspaceID := req.WorkspaceId
	contentID := req.ContentId

	// Audit emission (fire-and-forget)
	if p.auditHook != nil {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.auditHook(ctx, workspaceID, contentHash, decision, d.Category, d.Confidence, processingMs)
		}()
	}

	// NATS decision publish (fire-and-forget)
	if p.decisionHook != nil {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.decisionHook(ctx, workspaceID, contentHash, decision, d.Category, d.Confidence)
		}()
	}

	// Quarantine update (fire-and-forget)
	if p.quarantineHook != nil {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.quarantineHook(ctx, contentID, decision, d.Category, d.Confidence)
		}()
	}
}

// Wait blocks until all in-flight hook goroutines have completed.
// Useful for graceful shutdown and testing.
func (p *pipeline) Wait() {
	p.wg.Wait()
}
