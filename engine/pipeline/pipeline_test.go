package pipeline

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/soyAurelio/AurelioMod/engine/hasher"
	"github.com/soyAurelio/AurelioMod/internal/cache"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// --- mock implementations ---

// mockL1Cache implements cache.L1Cache for testing.
type mockL1Cache struct {
	decisions map[string]*cache.CachedDecision
	simError  error // if set, GetL1 simulates a DB error (returns nil, false)
}

func (m *mockL1Cache) GetL1(_ context.Context, blake3Hash string) (*cache.CachedDecision, bool) {
	if m.simError != nil {
		return nil, false
	}
	d, ok := m.decisions[blake3Hash]
	return d, ok
}

func (m *mockL1Cache) SetL1(_ context.Context, _ string, _ *cache.CachedDecision) error {
	return nil
}

// mockL2Cache implements cache.L2Cache for testing.
type mockL2Cache struct {
	decisions []*cache.CachedDecision
	simError  error
}

func (m *mockL2Cache) GetL2(_ context.Context, _ uint64, _ int) ([]*cache.CachedDecision, error) {
	if m.simError != nil {
		return nil, m.simError
	}
	return m.decisions, nil
}

func (m *mockL2Cache) SetL2(_ context.Context, _ uint64, _ *cache.CachedDecision) error {
	return nil
}

// mockNormalizer implements ContentNormalizer for testing.
type mockNormalizer struct {
	pixels []byte
	err    error
}

func (m *mockNormalizer) Normalize(_ []byte) (*hasher.NormalizeResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &hasher.NormalizeResult{
		RGBPixels: m.pixels,
		MIMEType:  "image/jpeg",
	}, nil
}

// --- helpers ---

// testPixels returns a deterministic 640×480 RGB24 gradient for testing.
// This produces consistent BLAKE3 and pHash values across test runs.
func testPixels() []byte {
	const w, h = 640, 480
	pixels := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (y*w + x) * 3
			pixels[idx] = byte(x % 256)       // R
			pixels[idx+1] = byte(y % 256)     // G
			pixels[idx+2] = byte((x + y) % 256) // B
		}
	}
	return pixels
}

// cachedBlock returns a pre-built CachedDecision with DECISION_BLOCK.
func cachedBlock() *cache.CachedDecision {
	return &cache.CachedDecision{
		Decision:   v1.Decision_DECISION_BLOCK,
		Confidence: 0.95,
		Category:   "violence_graphic",
	}
}

// cachedAllow returns a pre-built CachedDecision with DECISION_ALLOW.
func cachedAllow() *cache.CachedDecision {
	return &cache.CachedDecision{
		Decision:   v1.Decision_DECISION_ALLOW,
		Confidence: 0.99,
		Category:   "safe",
	}
}

// newTestPipeline creates a pipeline with mock caches and test pixel data.
func newTestPipeline(l1 *mockL1Cache, l2 *mockL2Cache) Pipeline {
	return New(l1, l2, &mockNormalizer{pixels: testPixels()})
}

// precomputeHash returns the BLAKE3 hex hash of testPixels() for cache setup.
func precomputeHash() string {
	return hasher.HashL1(testPixels())
}

// --- tests ---

func TestPipeline_L1Hit(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{
			blake3Hash: cachedBlock(),
		},
	}
	l2 := &mockL2Cache{}
	p := newTestPipeline(l1, l2)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF}, // minimal JPEG header
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3 {
		t.Errorf("CacheLevel = %v, want L1_BLAKE3", resp.CacheLevel)
	}
	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Decision)
	}
	if resp.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", resp.Confidence)
	}
	if resp.Category != "violence_graphic" {
		t.Errorf("Category = %q, want violence_graphic", resp.Category)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
	if resp.ProcessingMs < 0 {
		t.Error("ProcessingMs should be non-negative")
	}
}

func TestPipeline_L1Miss_L2Hit(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{}, // empty → L1 miss
	}
	l2 := &mockL2Cache{
		decisions: []*cache.CachedDecision{cachedAllow()},
	}
	p := newTestPipeline(l1, l2)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L2_PHASH {
		t.Errorf("CacheLevel = %v, want L2_PHASH", resp.CacheLevel)
	}
	if resp.Decision != v1.Decision_DECISION_ALLOW {
		t.Errorf("Decision = %v, want ALLOW", resp.Decision)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

func TestPipeline_L1Miss_L2Miss(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{}, // empty
	}
	l2 := &mockL2Cache{
		decisions: nil, // empty → L2 miss
	}
	p := newTestPipeline(l1, l2)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE", resp.CacheLevel)
	}
	if resp.Decision != v1.Decision_DECISION_QUEUED {
		t.Errorf("Decision = %v, want QUEUED (pending WaveSpeed)", resp.Decision)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
	if resp.ProcessingMs < 0 {
		t.Error("ProcessingMs should be non-negative even on miss")
	}
}

func TestPipeline_L2Error_GracefulDegradation(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{}, // L1 miss
	}
	l2 := &mockL2Cache{
		simError: errors.New("DragonflyDB connection refused"),
	}
	p := newTestPipeline(l1, l2)

	// Should NOT return an error — should degrade gracefully
	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should not error on L2 failure, got: %v", err)
	}

	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE (graceful degradation on L2 error)", resp.CacheLevel)
	}
	if resp.Decision != v1.Decision_DECISION_QUEUED {
		t.Errorf("Decision = %v, want QUEUED (fallback)", resp.Decision)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q (hash still computed)", resp.ContentHash, blake3Hash)
	}
}

func TestPipeline_L1Error_GracefulDegradation(t *testing.T) {
	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{},
		simError:  errors.New("connection timeout"),
	}
	l2 := &mockL2Cache{
		decisions: []*cache.CachedDecision{cachedBlock()}, // L2 has data
	}
	p := newTestPipeline(l1, l2)

	// L1 error should be treated as miss → falls through to L2
	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Falls through to L2
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L2_PHASH {
		t.Errorf("CacheLevel = %v, want L2_PHASH (L1 error → fallthrough to L2)", resp.CacheLevel)
	}
}

func TestPipeline_L1AndL2Error_GracefulDegradation(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{},
		simError:  errors.New("connection timeout"),
	}
	l2 := &mockL2Cache{
		simError: errors.New("connection refused"),
	}
	p := newTestPipeline(l1, l2)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should not error on full cache failure, got: %v", err)
	}

	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE (both caches unavailable)", resp.CacheLevel)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash should still be computed even with cache failures")
	}
}

func TestPipeline_NormalizeError(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}
	l2 := &mockL2Cache{}
	p := New(l1, l2, &mockNormalizer{err: errors.New("ffmpeg not found")})

	_, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err == nil {
		t.Fatal("Expected error on normalization failure, got nil")
	}
}

func TestPipeline_Timeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blake3Hash := precomputeHash()

		l1 := &mockL1Cache{
			decisions: map[string]*cache.CachedDecision{
				blake3Hash: cachedBlock(),
			},
		}
		l2 := &mockL2Cache{}
		p := newTestPipeline(l1, l2)

		ctx := t.Context()
		resp, err := p.Execute(ctx, &v1.AnalyzeRequest{
			WorkspaceId: "ws-test",
			RawBytes:    []byte{0xFF, 0xD8, 0xFF},
		})
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3 {
			t.Errorf("CacheLevel = %v, want L1_BLAKE3", resp.CacheLevel)
		}
	})
}
