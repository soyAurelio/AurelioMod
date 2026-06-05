// Package pipeline orchestrates content analysis through the L1→L2 cache hierarchy.
// Each layer is progressively more expensive but covers more types of matches:
//
//	L1: BLAKE3 exact match (<5ms)  → same content was analyzed before
//	L2: pHash perceptual match (<50ms) → visually similar content found
//
// On cache miss, the pipeline returns a QUEUED decision with computed hashes,
// ready for L3/WaveSpeed analysis (PR #3).
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/soyAurelio/AurelioMod/engine/hasher"
	"github.com/soyAurelio/AurelioMod/internal/cache"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// ContentNormalizer normalizes raw content bytes into decoded RGB24 pixel data.
// hasher.Normalizer satisfies this interface.
type ContentNormalizer interface {
	Normalize(input []byte) (*hasher.NormalizeResult, error)
}

// Pipeline orchestrates content analysis through cache layers.
type Pipeline interface {
	// Execute runs the full analysis pipeline on an AnalyzeRequest.
	// Returns an AnalyzeResponse with the cache level that produced the decision,
	// or CACHE_LEVEL_NONE with DECISION_QUEUED if all caches missed.
	Execute(ctx context.Context, req *v1.AnalyzeRequest) (*v1.AnalyzeResponse, error)
}

type pipeline struct {
	l1cache    cache.L1Cache
	l2cache    cache.L2Cache
	normalizer ContentNormalizer
}

// New creates a Pipeline backed by L1 and L2 caches and a content normalizer.
func New(l1 cache.L1Cache, l2 cache.L2Cache, norm ContentNormalizer) Pipeline {
	return &pipeline{
		l1cache:    l1,
		l2cache:    l2,
		normalizer: norm,
	}
}

// Execute runs the normalization → L1 → L2 cascade.
func (p *pipeline) Execute(ctx context.Context, req *v1.AnalyzeRequest) (*v1.AnalyzeResponse, error) {
	start := time.Now()

	// Step 1: Normalize raw bytes into RGB24 pixel data
	normalized, err := p.normalizer.Normalize(req.RawBytes)
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

	// Step 3: Check L1 cache (exact match)
	if d, ok := p.l1cache.GetL1(ctx, l1Hash); ok {
		slog.InfoContext(ctx, "L1 cache hit",
			"hash", l1Hash,
			"category", d.Category,
			"workspace_id", req.WorkspaceId,
		)
		return buildResponse(d, v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3, l1Hash, time.Since(start)), nil
	}

	// L1 miss — compute perceptual hash for L2
	ph := hasher.PHash(pixels)

	// Step 4: Check L2 cache (perceptual match within Hamming ≤ 5)
	results, err := p.l2cache.GetL2(ctx, ph, hasher.HammingThreshold)
	if err != nil {
		// DragonflyDB unavailable — log warning and proceed as miss
		slog.WarnContext(ctx, "L2 cache unavailable, proceeding as miss",
			"error", err,
			"phash", fmt.Sprintf("%016x", ph),
			"workspace_id", req.WorkspaceId,
		)
	} else if len(results) > 0 {
		slog.InfoContext(ctx, "L2 cache hit",
			"phash", fmt.Sprintf("%016x", ph),
			"category", results[0].Category,
			"workspace_id", req.WorkspaceId,
		)
		return buildResponse(results[0], v1.CacheLevel_CACHE_LEVEL_L2_PHASH, l1Hash, time.Since(start)), nil
	}

	// Both caches missed — return QUEUED for downstream analysis
	slog.InfoContext(ctx, "cache miss",
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
