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
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/soyAurelio/AurelioMod/engine/analyzer"
	"github.com/soyAurelio/AurelioMod/engine/hasher"
	"github.com/soyAurelio/AurelioMod/engine/telemetry"
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

// FrameExtractor downloads video from a URL and extracts frame bytes.
// Implementations handle YouTube via yt-dlp + FFmpeg.
// Returns the extracted PNG frame bytes.
type FrameExtractor func(ctx context.Context, videoURL string, timestampSec, maxFrames int) ([][]byte, error)

// URLChecker validates URLs for safety before content fetch.
// Implementations call Google Web Risk API or equivalent.
// Returns an error if the URL is malicious; nil if safe.
type URLChecker interface {
	CheckURL(ctx context.Context, rawURL string) error
}

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

// WithURLChecker sets the URL safety checker (Web Risk API).
// When set, all EXTERNAL_URL content is validated before content fetch.
// Malicious URLs return DECISION_BLOCK immediately.
func WithURLChecker(checker URLChecker) PipelineOption {
	return func(p *pipeline) { p.urlChecker = checker }
}

// WithFrameExtractor sets the frame extraction function for YouTube URL analysis.
// When nil (default), EXTERNAL_URL content falls through to standard analysis
// (URL is treated as text, not downloadable content).
func WithFrameExtractor(fe FrameExtractor) PipelineOption {
	return func(p *pipeline) { p.frameExtractor = fe }
}

// WithEngineMetrics sets the OpenTelemetry metrics recorder for the pipeline.
// Records: analysis_total, analysis_duration, cache_hits/misses per level.
func WithEngineMetrics(m *telemetry.EngineMetrics) PipelineOption {
	return func(p *pipeline) { p.engineMetrics = m }
}

type pipeline struct {
	l1cache        cache.L1Cache
	l2cache        cache.L2Cache
	normalizer     ContentNormalizer
	analyzer       analyzer.Analyzer
	weaviateClient weaviate.WeaviateClient

	// Optional capabilities
	urlChecker           URLChecker     // Web Risk API URL safety check
	frameExtractor       FrameExtractor // YouTube frame extraction (nil = disabled)
	extractFramesEnabled bool           // EXTRACT_FRAMES_ENABLED env gate

	// Integration hooks (fire-and-forget, non-blocking)
	auditHook      AuditHook
	decisionHook   DecisionHook
	quarantineHook QuarantineHook

	// Observability
	engineMetrics *telemetry.EngineMetrics

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
		l1cache:              l1,
		l2cache:              l2,
		normalizer:           norm,
		analyzer:             a,
		weaviateClient:       wv,
		extractFramesEnabled: envBool("EXTRACT_FRAMES_ENABLED", false),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Execute runs the normalization → L1 → L2 → L3 → WaveSpeed cascade.
//
// When content_type is CONTENT_TYPE_EXTERNAL_URL with a YouTube domain and
// EXTRACT_FRAMES_ENABLED=true, Execute extracts video frames via the configured
// FrameExtractor and analyzes each frame individually (spec R3.1).
func (p *pipeline) Execute(ctx context.Context, req *v1.AnalyzeRequest) (*v1.AnalyzeResponse, error) {
	start := time.Now()

	// Root span: pipeline.analyze
	tracer := otel.Tracer("pipeline")
	ctx, span := tracer.Start(ctx, "pipeline.analyze",
		trace.WithAttributes(
			attribute.String("workspace_id", req.WorkspaceId),
			attribute.String("content_type", req.ContentType.String()),
		),
	)
	defer span.End()

	// External URL path: check URL safety BEFORE any content fetch.
	// Malicious URLs are blocked immediately per NIS2/DSA requirements.
	if req.ContentType == v1.ContentType_CONTENT_TYPE_EXTERNAL_URL {
		rawURL := string(req.RawBytes)

		// Gate 1: URL reputation check (Web Risk API)
		if p.urlChecker != nil {
			if err := p.urlChecker.CheckURL(ctx, rawURL); err != nil {
				slog.WarnContext(ctx, "pipeline: url blocked by safety check",
					"url", rawURL,
					"reason", err.Error(),
					"workspace_id", req.WorkspaceId,
				)
				return &v1.AnalyzeResponse{
					Decision:     v1.Decision_DECISION_BLOCK,
					BlockReason:  fmt.Sprintf("url_safety:%s", err.Error()),
					Confidence:   1.0,
					Category:     "url_malicious",
					CacheLevel:   v1.CacheLevel_CACHE_LEVEL_NONE,
					ProcessingMs: time.Since(start).Milliseconds(),
				}, nil
			}
		}

		// Gate 2: Frame extraction path (YouTube, when enabled)
		if p.frameExtractor != nil && p.extractFramesEnabled && isYouTubeURL(rawURL) {
			ts, ok := parseTimestampParam(rawURL)
			if ok {
				slog.InfoContext(ctx, "youtube frame extraction triggered",
					"url", rawURL,
					"timestamp_sec", ts,
					"workspace_id", req.WorkspaceId,
				)
				return p.executeFrameExtraction(ctx, req, rawURL, ts, start)
			}
		}
	}

	// Standard path: normalize → L1 → L2 → L3 → WaveSpeed
	//
	// EXTERNAL_URL without YouTube frame extraction → QUEUED.
	// URLs need yt-dlp fetch + Web Risk before pixel-level analysis.
	// This avoids FFmpeg decode errors on URL text.
	if req.ContentType == v1.ContentType_CONTENT_TYPE_EXTERNAL_URL {
		return &v1.AnalyzeResponse{
			Decision:     v1.Decision_DECISION_QUEUED,
			BlockReason:  "pending_url_fetch",
			CacheLevel:   v1.CacheLevel_CACHE_LEVEL_NONE,
			ProcessingMs: time.Since(start).Milliseconds(),
		}, nil
	}

	return p.executeStandard(ctx, req, start)
}

// lastChanceRecheck performs a final cache sweep L1→L2→L3 after WaveSpeed
// has failed. Another concurrent request may have populated the caches while
// this request was blocked on the Bulkhead. If any level hits, the cached
// decision is returned with degraded_confidence set to the cached confidence.
//
// Returns (nil, false) if all caches miss — the caller should then return
// DECISION_ERROR with DegradedConfidence=0.
func (p *pipeline) lastChanceRecheck(ctx context.Context, l1Hash string, ph uint64, elapsed time.Duration) (*v1.AnalyzeResponse, bool) {
	// L1 re-check: exact BLAKE3 match
	if d, ok := p.l1cache.GetL1(ctx, l1Hash); ok {
		slog.InfoContext(ctx, "degraded fallback: L1 cache hit",
			"hash", l1Hash,
			"category", d.Category,
		)
		resp := buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3, l1Hash, elapsed)
		resp.DegradedConfidence = d.Confidence
		resp.Decision = d.Decision
		return resp, true
	}

	// L2 re-check: perceptual pHash match
	if results, err := p.l2cache.GetL2(ctx, ph, hasher.HammingThreshold); err != nil {
		slog.WarnContext(ctx, "degraded fallback: L2 cache unavailable",
			"error", err,
			"phash", fmt.Sprintf("%016x", ph),
		)
	} else if len(results) > 0 {
		slog.InfoContext(ctx, "degraded fallback: L2 cache hit",
			"phash", fmt.Sprintf("%016x", ph),
			"category", results[0].Category,
		)
		resp := buildResponse(results[0], v1.CacheLevel_CACHE_LEVEL_L2_PHASH, l1Hash, elapsed)
		resp.DegradedConfidence = results[0].Confidence
		resp.Decision = results[0].Decision
		return resp, true
	}

	// L3 re-check: Weaviate vector search
	if p.weaviateClient != nil {
		phVector := hasher.PHashVector(ph)
		if d, err := p.weaviateClient.SearchSimilar(ctx, phVector, 0.92); err != nil {
			slog.WarnContext(ctx, "degraded fallback: L3 Weaviate unavailable",
				"error", err,
			)
		} else if d != nil {
			slog.InfoContext(ctx, "degraded fallback: L3 cache hit",
				"content_hash", l1Hash,
				"category", d.Category,
			)
			resp := buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L3_WEAVIATE, l1Hash, elapsed)
			resp.DegradedConfidence = d.Confidence
			resp.Decision = d.Decision
			return resp, true
		}
	}

	return nil, false
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
		phVector := hasher.PHashVector(ph)
		if err := p.weaviateClient.IndexDecision(ctx, l1Hash, phVector, d); err != nil {
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

// --- External URL frame extraction (spec §3) ---

// isYouTubeURL returns true if the URL's host belongs to YouTube.
func isYouTubeURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return strings.Contains(host, "youtube.com") || strings.Contains(host, "youtu.be")
}

// parseTimestampParam extracts the t= query parameter in seconds.
// Reuses media.ParseTimestamp via inline implementation to avoid
// a package dependency cycle.
func parseTimestampParam(rawURL string) (int, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, false
	}
	raw := u.Query().Get("t")
	if raw == "" {
		return 0, false
	}
	if d, e := time.ParseDuration(raw); e == nil {
		if secs := int(d.Seconds()); secs >= 0 {
			return secs, true
		}
		return 0, false
	}
	// Fallback: raw integer
	var secs int
	if _, err := fmt.Sscanf(raw, "%d", &secs); err == nil && secs >= 0 {
		return secs, true
	}
	return 0, false
}

// executeFrameExtraction extracts video frames from a YouTube URL and analyzes
// each frame through the normal pipeline (normalize → L1 → L2 → L3 → WaveSpeed).
// Results are aggregated: the most severe decision across all frames wins,
// with the highest confidence reported (spec R3.4).
func (p *pipeline) executeFrameExtraction(
	ctx context.Context,
	req *v1.AnalyzeRequest,
	videoURL string,
	timestampSec int,
	start time.Time,
) (*v1.AnalyzeResponse, error) {
	const maxFrames = 3 // spec R3.3

	frames, err := p.frameExtractor(ctx, videoURL, timestampSec, maxFrames)
	if err != nil {
		slog.ErrorContext(ctx, "frame extraction failed",
			"url", videoURL,
			"error", err,
			"workspace_id", req.WorkspaceId,
		)
		return nil, fmt.Errorf("pipeline frame extraction: %w", err)
	}

	if len(frames) == 0 {
		slog.WarnContext(ctx, "frame extraction produced zero frames",
			"url", videoURL,
			"workspace_id", req.WorkspaceId,
		)
		// Fall through to standard analysis with URL as content
		return p.executeStandard(ctx, req, start)
	}

	// Analyze each frame individually through the full pipeline
	var (
		worstDecision v1.Decision
		worstCategory string
		highestConf   float64
		anyFrameHit   bool
		aggregateHash string
	)

	for i, frame := range frames {
		frameReq := &v1.AnalyzeRequest{
			WorkspaceId:    req.WorkspaceId,
			ContentId:      fmt.Sprintf("%s-frame-%d", req.ContentId, i),
			RawBytes:       frame,
			ContentType:    v1.ContentType_CONTENT_TYPE_IMAGE,
			SourcePlatform: req.SourcePlatform,
		}

		resp, err := p.executeStandard(ctx, frameReq, time.Now())
		if err != nil {
			slog.WarnContext(ctx, "frame analysis failed, skipping",
				"frame", i,
				"error", err,
			)
			continue
		}

		anyFrameHit = true
		aggregateHash = resp.ContentHash

		// Keep the worst decision (BLOCK > ERROR > QUEUED > ALLOW)
		if isMoreSevere(resp.Decision, worstDecision) {
			worstDecision = resp.Decision
			worstCategory = resp.Category
		}

		// Track highest confidence
		if resp.Confidence > highestConf {
			highestConf = resp.Confidence
		}
	}

	if !anyFrameHit {
		// Total failure on all frames (spec R3.6: partial results)
		return &v1.AnalyzeResponse{
			Decision:     v1.Decision_DECISION_ERROR,
			ContentHash:  aggregateHash,
			CacheLevel:   v1.CacheLevel_CACHE_LEVEL_NONE,
			ProcessingMs: time.Since(start).Milliseconds(),
		}, nil
	}

	return &v1.AnalyzeResponse{
		Decision:     worstDecision,
		BlockReason:  worstCategory,
		Confidence:   highestConf,
		Category:     worstCategory,
		ContentHash:  aggregateHash,
		CacheLevel:   v1.CacheLevel_CACHE_LEVEL_NONE,
		ProcessingMs: time.Since(start).Milliseconds(),
	}, nil
}

// executeStandard runs the standard pipeline from normalize through WaveSpeed.
// Extracted as a separate method to be reusable from the frame extraction path.
func (p *pipeline) executeStandard(ctx context.Context, req *v1.AnalyzeRequest, start time.Time) (*v1.AnalyzeResponse, error) {
	tracer := otel.Tracer("pipeline")

	normalized, err := p.normalizer.Normalize(ctx, req.RawBytes)
	if err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}
	pixels := normalized.RGBPixels
	l1Hash := hasher.HashL1(pixels)
	ph := hasher.PHash(pixels)

	// L1: BLAKE3 exact match
	_, l1Span := tracer.Start(ctx, "cache.L1_check",
		trace.WithAttributes(attribute.String("hash", l1Hash[:16])),
	)
	if d, ok := p.l1cache.GetL1(ctx, l1Hash); ok {
		l1Span.SetAttributes(attribute.Bool("hit", true))
		l1Span.End()
		elapsed := time.Since(start)
		p.fireHooks(ctx, req, l1Hash, d, elapsed.Milliseconds())
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String("cache_level", "L1"),
			attribute.Bool("cache_hit", true),
		)
		return buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3, l1Hash, elapsed), nil
	}
	l1Span.End()

	// L2: pHash perceptual match
	_, l2Span := tracer.Start(ctx, "cache.L2_check")
	results, err := p.l2cache.GetL2(ctx, ph, hasher.HammingThreshold)
	if err != nil {
		slog.WarnContext(ctx, "L2 cache unavailable", "error", err)
		l2Span.SetAttributes(attribute.Bool("error", true))
	} else if len(results) > 0 {
		l2Span.SetAttributes(attribute.Bool("hit", true), attribute.Int("candidates", len(results)))
		l2Span.End()
		elapsed := time.Since(start)
		p.fireHooks(ctx, req, l1Hash, results[0], elapsed.Milliseconds())
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String("cache_level", "L2"),
			attribute.Bool("cache_hit", true),
		)
		return buildResponse(results[0], v1.CacheLevel_CACHE_LEVEL_L2_PHASH, l1Hash, elapsed), nil
	}
	l2Span.End()

	// L3: Weaviate vector search
	if p.weaviateClient != nil {
		_, l3Span := tracer.Start(ctx, "cache.L3_check")
		phVector := hasher.PHashVector(ph)
		if d, err := p.weaviateClient.SearchSimilar(ctx, phVector, 0.92); err != nil {
			slog.WarnContext(ctx, "L3 unavailable", "error", err)
			l3Span.SetAttributes(attribute.Bool("error", true))
		} else if d != nil {
			l3Span.SetAttributes(attribute.Bool("hit", true))
			l3Span.End()
			elapsed := time.Since(start)
			p.fireHooks(ctx, req, l1Hash, d, elapsed.Milliseconds())
			trace.SpanFromContext(ctx).SetAttributes(
				attribute.String("cache_level", "L3"),
				attribute.Bool("cache_hit", true),
			)
			return buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L3_WEAVIATE, l1Hash, elapsed), nil
		}
		l3Span.End()
	}

	if p.analyzer == nil {
		return &v1.AnalyzeResponse{
			Decision:     v1.Decision_DECISION_QUEUED,
			ContentHash:  l1Hash,
			CacheLevel:   v1.CacheLevel_CACHE_LEVEL_NONE,
			ProcessingMs: time.Since(start).Milliseconds(),
		}, nil
	}

	// Send normalized JPEG as Base64 data URI — WaveSpeed downloads from URL,
	// but we don't have a public URL until R2/MinIO upload. Base64 data URI
	// avoids the fake "storage.aureliomod.dev" URL that doesn't resolve.
	imageURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(normalized.JPEGBytes)
	result, err := p.analyzer.Analyze(ctx, imageURI, "image/jpeg")
	if err != nil {
		if degradeResp, degradedOk := p.lastChanceRecheck(ctx, l1Hash, ph, time.Since(start)); degradedOk {
			return degradeResp, nil
		}
		return &v1.AnalyzeResponse{
			Decision:           v1.Decision_DECISION_ERROR,
			ContentHash:        l1Hash,
			CacheLevel:         v1.CacheLevel_CACHE_LEVEL_NONE,
			DegradedConfidence: 0,
			ProcessingMs:       time.Since(start).Milliseconds(),
		}, nil
	}

	cached := &cache.CachedDecision{
		Decision:   decisionFromBool(result.Decision),
		Confidence: result.Confidence,
		Category:   dominantCategory(result.Categories),
	}

	p.backPopulate(ctx, l1Hash, ph, cached)
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

// isMoreSevere returns true if newIs is more severe than current.
// Severity: BLOCK(2) > ERROR(4) > QUEUED(3) > ALLOW(1) > UNSPECIFIED(0)
func isMoreSevere(newDec, currentDec v1.Decision) bool {
	severity := map[v1.Decision]int{
		v1.Decision_DECISION_UNSPECIFIED: 0,
		v1.Decision_DECISION_ALLOW:       1,
		v1.Decision_DECISION_QUEUED:      2,
		v1.Decision_DECISION_ERROR:       3,
		v1.Decision_DECISION_BLOCK:       4,
	}
	return severity[newDec] > severity[currentDec]
}

// envBool reads a boolean environment variable with a default value.
func envBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	val = strings.ToLower(strings.TrimSpace(val))
	return val == "true" || val == "1" || val == "yes" || val == "on"
}
