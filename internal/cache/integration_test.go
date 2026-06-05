//go:build integration

package cache

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/soyAurelio/AurelioMod/internal/testutil"
	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// dragonflyTestAddr holds the DragonflyDB address resolved by testcontainers
// during TestMain. It is set once before tests run.
var dragonflyTestAddr string

// TestMain starts DragonflyDB via testcontainers before running integration tests.
// When Docker is unavailable or -short is set, all tests are skipped gracefully.
func TestMain(m *testing.M) {
	flag.Parse()

	// Skip ALL integration tests when -short is set
	if testing.Short() {
		os.Exit(0)
	}

	// Start DragonflyDB via testcontainers (reused across all cache integration tests)
	ctx := context.Background()
	rdb, err := testutil.StartDragonfly(ctx)
	if err != nil || rdb == nil {
		// Docker not available or container startup failed — skip tests gracefully
		os.Exit(0)
	}
	dragonflyTestAddr = rdb.Options().Addr
	rdb.Close()

	os.Exit(m.Run())
}

// TestIntegration_L1CacheRoundTrip tests L1 cache against a real DragonflyDB instance.
func TestIntegration_L1CacheRoundTrip(t *testing.T) {
	addr := getDragonflyAddr(t)
	client := NewCacheClient(CacheClientConfig{Addr: addr})
	ctx := t.Context()

	blake3Hash := "integration-test-l1-" + t.Name()
	decision := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.99,
		Category:   "integration_test",
		CachedAt:   time.Now().UTC(),
	}

	// Set
	if err := client.SetL1(ctx, blake3Hash, decision); err != nil {
		t.Fatalf("SetL1: %v", err)
	}

	// Get — hit
	got, ok := client.GetL1(ctx, blake3Hash)
	if !ok {
		t.Fatal("expected L1 cache hit")
	}
	if got.Decision != decision.Decision {
		t.Errorf("Decision = %v, want %v", got.Decision, decision.Decision)
	}

	// Cleanup
	client.rdb.Del(ctx, "l1:"+blake3Hash)
}

// TestIntegration_L2CacheNearMatch tests L2 perceptual cache against real DragonflyDB.
func TestIntegration_L2CacheNearMatch(t *testing.T) {
	addr := getDragonflyAddr(t)
	client := NewCacheClient(CacheClientConfig{Addr: addr})
	ctx := t.Context()

	// Store two pHashes that differ by 2 bits
	ph1 := uint64(0xABCD000000000000)
	ph2 := uint64(0xABCD000000000003)

	d1 := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_ALLOW,
		Confidence: 0.90,
		Category:   "safe",
		CachedAt:   time.Now().UTC(),
	}
	d2 := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.85,
		Category:   "nsfw",
		CachedAt:   time.Now().UTC(),
	}

	if err := client.SetL2(ctx, ph1, d1); err != nil {
		t.Fatalf("SetL2 ph1: %v", err)
	}
	if err := client.SetL2(ctx, ph2, d2); err != nil {
		t.Fatalf("SetL2 ph2: %v", err)
	}

	// Query ph1 with threshold 5 — should find both
	results, err := client.GetL2(ctx, ph1, 5)
	if err != nil {
		t.Fatalf("GetL2: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Cleanup
	client.rdb.Del(ctx, "l2:00000000abcd0000", "l2:00000000abcd0003")
}

// TestIntegration_L2CacheMiss tests that distant pHashes don't match.
func TestIntegration_L2CacheMiss(t *testing.T) {
	addr := getDragonflyAddr(t)
	client := NewCacheClient(CacheClientConfig{Addr: addr})
	ctx := t.Context()

	// Store one pHash
	_ = client.SetL2(ctx, 0xFFFFFFFFFFFFFFFF, &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_ALLOW,
		Confidence: 1.0,
		Category:   "far",
		CachedAt:   time.Now().UTC(),
	})

	// Query with very different pHash at threshold 1 — should miss
	results, err := client.GetL2(ctx, 0x0000000000000000, 1)
	if err != nil {
		t.Fatalf("GetL2: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}

	// Cleanup
	client.rdb.Del(ctx, "l2:ffffffffffffffff")
}

// getDragonflyAddr returns the DragonflyDB address set during TestMain.
// Falls back to env var or default for backwards compatibility.
func getDragonflyAddr(t *testing.T) string {
	t.Helper()
	if dragonflyTestAddr != "" {
		return dragonflyTestAddr
	}
	addr := os.Getenv("DRAGONFLY_ADDR")
	if addr == "" {
		addr = "localhost:6380"
	}
	return addr
}
