package cache

import (
	"encoding/json"
	"math/bits"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

func TestNewCacheClient_Defaults(t *testing.T) {
	// Verify CacheClient creation with only required fields
	client := NewCacheClient(CacheClientConfig{
		Addr: "localhost:6379",
	})

	if client == nil {
		t.Fatal("NewCacheClient returned nil")
	}
}

func TestNewCacheClient_ExplicitPoolSize(t *testing.T) {
	client := NewCacheClient(CacheClientConfig{
		Addr:     "localhost:6379",
		PoolSize: 50,
	})

	if client == nil {
		t.Fatal("NewCacheClient returned nil")
	}
}

func TestCacheClient_L1SetGet_Hit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestClient(t, mr)

	ctx := t.Context()
	blake3Hash := "abc123def456"
	decision := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.95,
		Category:   "violence",
		CachedAt:   time.Now().UTC(),
	}

	// Set
	err := client.SetL1(ctx, blake3Hash, decision)
	if err != nil {
		t.Fatalf("SetL1 error: %v", err)
	}

	// Get
	got, ok := client.GetL1(ctx, blake3Hash)
	if !ok {
		t.Fatal("GetL1: expected cache hit, got miss")
	}
	if got.Decision != decision.Decision {
		t.Errorf("Decision = %v, want %v", got.Decision, decision.Decision)
	}
	if got.Confidence != decision.Confidence {
		t.Errorf("Confidence = %v, want %v", got.Confidence, decision.Confidence)
	}
	if got.Category != decision.Category {
		t.Errorf("Category = %q, want %q", got.Category, decision.Category)
	}
}

func TestCacheClient_L1Get_Miss(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestClient(t, mr)

	_, ok := client.GetL1(t.Context(), "nonexistent")
	if ok {
		t.Fatal("GetL1: expected cache miss, got hit")
	}
}

func TestCacheClient_L1Set_Overwrite(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestClient(t, mr)
	ctx := t.Context()

	first := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_ALLOW,
		Confidence: 0.80,
		Category:   "safe",
		CachedAt:   time.Now().UTC(),
	}
	second := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.99,
		Category:   "violence",
		CachedAt:   time.Now().UTC(),
	}

	_ = client.SetL1(ctx, "key1", first)
	_ = client.SetL1(ctx, "key1", second)

	got, ok := client.GetL1(ctx, "key1")
	if !ok {
		t.Fatal("GetL1: expected hit after overwrite")
	}
	if got.Decision != second.Decision {
		t.Errorf("after overwrite: Decision = %v, want %v", got.Decision, second.Decision)
	}
}

func TestCacheClient_L2SetGet_Hit(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestClient(t, mr)
	ctx := t.Context()

	pHash := uint64(0xDEADBEEF)
	decision := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.88,
		Category:   "nsfw",
		CachedAt:   time.Now().UTC(),
	}

	err := client.SetL2(ctx, pHash, decision)
	if err != nil {
		t.Fatalf("SetL2 error: %v", err)
	}

	// GetL2 with Hamming distance 5 — should find our exact match
	results, err := client.GetL2(ctx, pHash, 5)
	if err != nil {
		t.Fatalf("GetL2 error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("GetL2: expected at least 1 result for exact match")
	}
	if results[0].Decision != decision.Decision {
		t.Errorf("Decision = %v, want %v", results[0].Decision, decision.Decision)
	}
}

func TestCacheClient_L2Get_NearMatch(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestClient(t, mr)
	ctx := t.Context()

	// Store two pHash values that differ by 2 bits
	ph1 := uint64(0x0000000000000000)
	ph2 := uint64(0x0000000000000003) // bits 0 and 1 set

	_ = client.SetL2(ctx, ph1, &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_ALLOW,
		Confidence: 0.90,
		Category:   "safe",
		CachedAt:   time.Now().UTC(),
	})
	_ = client.SetL2(ctx, ph2, &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.85,
		Category:   "nsfw",
		CachedAt:   time.Now().UTC(),
	})

	// Query ph1 with threshold 5 — should find ph2 (H dist 2)
	results, err := client.GetL2(ctx, ph1, 5)
	if err != nil {
		t.Fatalf("GetL2 error: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("GetL2: expected at least 2 results, got %d", len(results))
	}
}

func TestCacheClient_L2Get_Miss(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestClient(t, mr)
	ctx := t.Context()

	_ = client.SetL2(ctx, 0xFFFFFFFFFFFFFFFF, &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 1.0,
		Category:   "test",
		CachedAt:   time.Now().UTC(),
	})

	// Query with a very different hash — should miss at threshold 1
	results, err := client.GetL2(ctx, 0x0000000000000000, 1)
	if err != nil {
		t.Fatalf("GetL2 error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("GetL2: expected 0 results for Hamming dist > 1, got %d", len(results))
	}
}

func TestCacheClient_L2Results_SortedByDistance(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestClient(t, mr)
	ctx := t.Context()

	// Store at varying Hamming distances from query (0 bits set)
	query := uint64(0)
	cases := []struct {
		phash uint64
		cat   string
		dist  int // expected Hamming distance
	}{
		{0x000000000000000F, "far", 4},    // 4 bits
		{0x0000000000000001, "near", 1},   // 1 bit
		{0x0000000000000003, "medium", 2}, // 2 bits
	}
	for _, c := range cases {
		_ = client.SetL2(ctx, c.phash, &CachedDecision{
			Decision:   aureliomodv1.Decision_DECISION_BLOCK,
			Confidence: 0.50,
			Category:   c.cat,
			CachedAt:   time.Now().UTC(),
		})
	}

	results, err := client.GetL2(ctx, query, 5)
	if err != nil {
		t.Fatalf("GetL2 error: %v", err)
	}

	// Verify sorted by Hamming distance (ascending)
	for i := 1; i < len(results); i++ {
		prev := binaryHash(results[i-1].Category) // use category as pHash proxy
		curr := binaryHash(results[i].Category)
		prevDist := bits.OnesCount64(prev ^ query)
		currDist := bits.OnesCount64(curr ^ query)
		if prevDist > currDist {
			t.Errorf("results not sorted by distance: result[%d] dist=%d, result[%d] dist=%d",
				i-1, prevDist, i, currDist)
		}
	}
}

func TestCachedDecision_JSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	original := &CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.94,
		Category:   "violence_graphic",
		CachedAt:   now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored CachedDecision
	err = json.Unmarshal(data, &restored)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if restored.Decision != original.Decision {
		t.Errorf("Decision roundtrip failed: %v != %v", restored.Decision, original.Decision)
	}
	if restored.Confidence != original.Confidence {
		t.Errorf("Confidence roundtrip failed: %v != %v", restored.Confidence, original.Confidence)
	}
	if restored.Category != original.Category {
		t.Errorf("Category roundtrip failed: %q != %q", restored.Category, original.Category)
	}
	if !restored.CachedAt.Equal(original.CachedAt) {
		t.Errorf("CachedAt roundtrip failed: %v != %v", restored.CachedAt, original.CachedAt)
	}
}

func TestHammingDistance_Simple(t *testing.T) {
	tests := []struct {
		a, b uint64
		want int
	}{
		{0, 0, 0},
		{0xFFFFFFFFFFFFFFFF, 0, 64},
		{0x0000000000000001, 0x0000000000000001, 0},
		{0x0000000000000001, 0x0000000000000002, 2}, // different bits
		{0x000000000000000F, 0, 4},                    // 4 bits set
	}

	for _, tt := range tests {
		got := bits.OnesCount64(tt.a ^ tt.b)
		if got != tt.want {
			t.Errorf("Hamming(%016x, %016x) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// --- helpers ---

// binaryHash uses the Category field as a placeholder for pHash in tests.
// This is a test-only helper for the sorted-by-distance test.
func binaryHash(cat string) uint64 {
	switch cat {
	case "far":
		return 0x000000000000000F
	case "near":
		return 0x0000000000000001
	case "medium":
		return 0x0000000000000003
	default:
		return 0
	}
}

// newTestClient creates a CacheClient connected to a miniredis instance.
func newTestClient(t *testing.T, mr *miniredis.Miniredis) *CacheClient {
	t.Helper()

	opts, err := redis.ParseURL("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("miniredis parse addr: %v", err)
	}

	return &CacheClient{
		rdb: redis.NewClient(opts),
	}
}
