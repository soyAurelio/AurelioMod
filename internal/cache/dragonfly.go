package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/bits"

	"github.com/redis/go-redis/v9"
)

// CacheClientConfig holds connection parameters for the DragonflyDB cache client.
type CacheClientConfig struct {
	// Addr is the DragonflyDB address (e.g., "localhost:6380").
	Addr string

	// PoolSize is the maximum number of connections in the pool.
	// Default: 200 (Engine pool size per project standards).
	PoolSize int

	// Password for authenticated connections. Empty for dev.
	Password string

	// DB is the Redis database number. Default: 0.
	DB int
}

// CacheClient wraps a go-redis client for L1 (BLAKE3) and L2 (pHash) cache operations.
// It implements both L1Cache and L2Cache interfaces.
type CacheClient struct {
	rdb *redis.Client
}

// Compile-time interface checks.
var (
	_ L1Cache = (*CacheClient)(nil)
	_ L2Cache = (*CacheClient)(nil)
)

// NewCacheClient creates a CacheClient connected to DragonflyDB.
func NewCacheClient(cfg CacheClientConfig) *CacheClient {
	poolSize := cfg.PoolSize
	if poolSize <= 0 {
		poolSize = 200 // Engine default
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: poolSize,
	})

	return &CacheClient{rdb: rdb}
}

// redisKeyL1 returns the L1 cache key for a BLAKE3 hex hash.
func redisKeyL1(blake3Hash string) string {
	return "l1:" + blake3Hash
}

// redisKeyL2 returns the L2 cache key for a pHash value.
func redisKeyL2(pHash uint64) string {
	return fmt.Sprintf("l2:%016x", pHash)
}

// GetL1 retrieves a cached decision by BLAKE3 hex hash.
// Returns (nil, false) on cache miss or error.
func (c *CacheClient) GetL1(ctx context.Context, blake3Hash string) (*CachedDecision, bool) {
	data, err := c.rdb.Get(ctx, redisKeyL1(blake3Hash)).Bytes()
	if err != nil {
		if err != redis.Nil {
			slog.WarnContext(ctx, "L1 cache get error", "key", blake3Hash, "error", err)
		}
		return nil, false
	}

	var d CachedDecision
	if err := json.Unmarshal(data, &d); err != nil {
		slog.ErrorContext(ctx, "L1 cache deserialization error", "key", blake3Hash, "error", err)
		return nil, false
	}

	return &d, true
}

// SetL1 stores a cached decision keyed by BLAKE3 hex hash.
func (c *CacheClient) SetL1(ctx context.Context, blake3Hash string, decision *CachedDecision) error {
	data, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("L1 marshal: %w", err)
	}

	return c.rdb.Set(ctx, redisKeyL1(blake3Hash), data, 0).Err()
}

// l2Candidate holds a pHash match candidate with its Hamming distance.
type l2Candidate struct {
	decision *CachedDecision
	distance int
}

// GetL2 retrieves cached decisions within hammingThreshold of the query pHash.
// Results are sorted by Hamming distance (nearest first).
// Returns empty slice on cache miss (not an error).
func (c *CacheClient) GetL2(ctx context.Context, pHash uint64, hammingThreshold int) ([]*CachedDecision, error) {
	var candidates []l2Candidate

	// Scan L2 keys using SCAN (NEVER KEYS *)
	iter := c.rdb.Scan(ctx, 0, "l2:*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		data, err := c.rdb.Get(ctx, key).Bytes()
		if err != nil {
			continue // skip stale/error keys
		}

		// Extract pHash from key: "l2:000000000000dead"
		var storedPHash uint64
		_, scanErr := fmt.Sscanf(key, "l2:%016x", &storedPHash)
		if scanErr != nil {
			continue
		}

		dist := bits.OnesCount64(pHash ^ storedPHash)
		if dist > hammingThreshold {
			continue
		}

		var d CachedDecision
		if err := json.Unmarshal(data, &d); err != nil {
			slog.WarnContext(ctx, "L2 cache deserialization error", "key", key, "error", err)
			continue
		}

		candidates = append(candidates, l2Candidate{decision: &d, distance: dist})
	}

	if err := iter.Err(); err != nil {
		slog.WarnContext(ctx, "L2 cache scan error", "error", err)
		// Return partial results even on scan error
	}

	// Sort by Hamming distance (ascending)
	sortCandidates(candidates)

	results := make([]*CachedDecision, len(candidates))
	for i, c := range candidates {
		results[i] = c.decision
	}

	return results, nil
}

// SetL2 stores a cached decision keyed by pHash.
func (c *CacheClient) SetL2(ctx context.Context, pHash uint64, decision *CachedDecision) error {
	data, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("L2 marshal: %w", err)
	}

	return c.rdb.Set(ctx, redisKeyL2(pHash), data, 0).Err()
}

// sortCandidates orders candidates by Hamming distance ascending (nearest first).
func sortCandidates(candidates []l2Candidate) {
	// Simple insertion sort — n is small (L2 cache is scoped)
	for i := 1; i < len(candidates); i++ {
		j := i
		for j > 0 && candidates[j].distance < candidates[j-1].distance {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
			j--
		}
	}
}
