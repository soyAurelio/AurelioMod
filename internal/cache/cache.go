// Package cache provides DragonflyDB-backed cache interfaces and implementations
// for the AurelioMod L1 (BLAKE3) and L2 (pHash) cache layers.
package cache

import (
	"context"
	"time"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// CachedDecision is a stored moderation decision returned from cache layers.
type CachedDecision struct {
	// Decision is the moderation outcome.
	Decision aureliomodv1.Decision

	// Confidence is the AI model confidence score (0.0 to 1.0).
	Confidence float64

	// Category is the classification category (e.g., "violence_graphic").
	Category string

	// CachedAt is when the decision was originally cached.
	CachedAt time.Time
}

// L1Cache is the exact-match cache layer keyed by BLAKE3 content hash.
// A hit means the exact same content was analyzed before.
type L1Cache interface {
	// GetL1 retrieves a cached decision by BLAKE3 hex hash.
	// Returns (nil, false) on cache miss.
	GetL1(ctx context.Context, blake3Hash string) (*CachedDecision, bool)

	// SetL1 stores a decision keyed by BLAKE3 hex hash.
	SetL1(ctx context.Context, blake3Hash string, decision *CachedDecision) error
}

// L2Cache is the perceptual-match cache layer keyed by pHash (perceptual hash).
// Matches are found by Hamming distance ≤ threshold from the query hash.
type L2Cache interface {
	// GetL2 retrieves cached decisions with Hamming distance ≤ hammingThreshold
	// from the query pHash. Returns results sorted by distance (nearest first).
	// Returns empty slice on cache miss (not an error).
	GetL2(ctx context.Context, pHash uint64, hammingThreshold int) ([]*CachedDecision, error)

	// SetL2 stores a decision keyed by pHash.
	SetL2(ctx context.Context, pHash uint64, decision *CachedDecision) error
}
