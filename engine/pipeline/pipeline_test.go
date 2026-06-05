package pipeline

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/soyAurelio/AurelioMod/engine/analyzer"
	"github.com/soyAurelio/AurelioMod/engine/hasher"
	"github.com/soyAurelio/AurelioMod/internal/cache"
	"github.com/soyAurelio/AurelioMod/internal/weaviate"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// --- mock implementations ---

// mockL1Cache implements cache.L1Cache for testing.
type mockL1Cache struct {
	decisions map[string]*cache.CachedDecision
	simError  error // if set, GetL1 simulates a DB error (returns nil, false)
	setL1Func func(ctx context.Context, hash string, d *cache.CachedDecision) error
}

func (m *mockL1Cache) GetL1(_ context.Context, blake3Hash string) (*cache.CachedDecision, bool) {
	if m.simError != nil {
		return nil, false
	}
	d, ok := m.decisions[blake3Hash]
	return d, ok
}

func (m *mockL1Cache) SetL1(ctx context.Context, hash string, d *cache.CachedDecision) error {
	if m.setL1Func != nil {
		return m.setL1Func(ctx, hash, d)
	}
	return nil
}

// mockL2Cache implements cache.L2Cache for testing.
type mockL2Cache struct {
	decisions []*cache.CachedDecision
	simError  error
	setL2Func func(ctx context.Context, phash uint64, d *cache.CachedDecision) error
}

func (m *mockL2Cache) GetL2(_ context.Context, _ uint64, _ int) ([]*cache.CachedDecision, error) {
	if m.simError != nil {
		return nil, m.simError
	}
	return m.decisions, nil
}

func (m *mockL2Cache) SetL2(ctx context.Context, phash uint64, d *cache.CachedDecision) error {
	if m.setL2Func != nil {
		return m.setL2Func(ctx, phash, d)
	}
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
// Analyzer and WeaviateClient are nil (L3+WaveSpeed layers skipped).
func newTestPipeline(l1 *mockL1Cache, l2 *mockL2Cache) Pipeline {
	return New(l1, l2, &mockNormalizer{pixels: testPixels()}, nil, nil)
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
	p := New(l1, l2, &mockNormalizer{err: errors.New("ffmpeg not found")}, nil, nil)

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

// --- L3 + WaveSpeed mock implementations ---

// mockAnalyzer implements analyzer.Analyzer for pipeline testing.
type mockAnalyzer struct {
	result *analyzer.ModerationResult
	err    error
}

var _ analyzer.Analyzer = (*mockAnalyzer)(nil)

func (m *mockAnalyzer) Analyze(_ context.Context, _ string, _ string) (*analyzer.ModerationResult, error) {
	return m.result, m.err
}

// mockWvClient implements weaviate.WeaviateClient for pipeline testing.
type mockWvClient struct {
	cachedDecision *cache.CachedDecision
	err            error
	lastIndexed    *cache.CachedDecision
	lastHash       string
}

var _ weaviate.WeaviateClient = (*mockWvClient)(nil)

func (m *mockWvClient) SearchSimilar(_ context.Context, _ string, _ float32) (*cache.CachedDecision, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.cachedDecision, nil
}

func (m *mockWvClient) IndexDecision(_ context.Context, contentHash string, decision *cache.CachedDecision) error {
	m.lastIndexed = decision
	m.lastHash = contentHash
	return nil
}

// newFullPipeline creates a pipeline with all 5 dependencies for L3/WaveSpeed tests.
func newFullPipeline(l1 *mockL1Cache, l2 *mockL2Cache, a *mockAnalyzer, wv *mockWvClient) Pipeline {
	return New(l1, l2, &mockNormalizer{pixels: testPixels()}, a, wv)
}

// --- L3 + WaveSpeed tests ---

// TestPipeline_L3Hit tests: L1 miss → L2 miss → L3 hit from Weaviate.
func TestPipeline_L3Hit(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // miss
	l2 := &mockL2Cache{decisions: nil}                                // miss
	wv := &mockWvClient{
		cachedDecision: cachedBlock(), // L3 hit
	}
	a := &mockAnalyzer{} // not called
	p := newFullPipeline(l1, l2, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L3_WEAVIATE {
		t.Errorf("CacheLevel = %v, want L3_WEAVIATE", resp.CacheLevel)
	}
	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Decision)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

// TestPipeline_L3Miss_WaveSpeed_Hit tests: L1+L2+L3 all miss → WaveSpeed returns decision.
func TestPipeline_L3Miss_WaveSpeed_Hit(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // miss
	l2 := &mockL2Cache{decisions: nil}                                // miss
	wv := &mockWvClient{cachedDecision: nil}                          // L3 miss
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     true,
			Confidence:   0.93,
			Categories:   map[string]bool{"violence": true},
			ProcessingMs: 200,
		},
	}
	p := newFullPipeline(l1, l2, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE (WaveSpeed fresh analysis)", resp.CacheLevel)
	}
	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Decision)
	}
	if resp.Confidence != 0.93 {
		t.Errorf("Confidence = %f, want 0.93", resp.Confidence)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

// TestPipeline_WaveSpeed_CleanDecision verifies WaveSpeed "clean" result
// is stored in all caches (L1, L2, L3 back-population).
func TestPipeline_WaveSpeed_CleanDecision(t *testing.T) {
	setL1Called := false
	setL2Called := false

	l1 := &mockL1Cache{
		decisions:  map[string]*cache.CachedDecision{},
		setL1Func:  func(_ context.Context, _ string, _ *cache.CachedDecision) error { setL1Called = true; return nil },
	}
	l2 := &mockL2Cache{
		decisions:  nil,
		setL2Func:  func(_ context.Context, _ uint64, _ *cache.CachedDecision) error { setL2Called = true; return nil },
	}
	wv := &mockWvClient{cachedDecision: nil} // L3 miss
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     false, // clean
			Confidence:   0.99,
			Categories:   map[string]bool{},
			ProcessingMs: 50,
		},
	}
	p := newFullPipeline(l1, l2, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if resp.Decision != v1.Decision_DECISION_ALLOW {
		t.Errorf("Decision = %v, want ALLOW", resp.Decision)
	}
	if resp.Confidence != 0.99 {
		t.Errorf("Confidence = %f, want 0.99", resp.Confidence)
	}

	// Verify back-population happened
	if !setL1Called {
		t.Error("Expected SetL1 to be called for back-population")
	}
	if !setL2Called {
		t.Error("Expected SetL2 to be called for back-population")
	}
	if wv.lastIndexed == nil {
		t.Error("Expected IndexDecision to be called for L3 back-population")
	}
}

// TestPipeline_WaveSpeedError_CircuitBreakerOpen tests: WaveSpeed error → pipeline returns error.
func TestPipeline_WaveSpeedError_CircuitBreakerOpen(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // miss
	l2 := &mockL2Cache{decisions: nil}                                // miss
	wv := &mockWvClient{cachedDecision: nil}                          // L3 miss
	a := &mockAnalyzer{
		err: errors.New("circuit breaker open: WaveSpeed unavailable"),
	}
	p := newFullPipeline(l1, l2, a, wv)

	_, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err == nil {
		t.Fatal("Expected error when WaveSpeed is unavailable, got nil")
	}
}

// TestPipeline_L3Unavailable_SkipToWaveSpeed tests: Weaviate unavailable →
// skip L3, go directly to WaveSpeed.
func TestPipeline_L3Unavailable_SkipToWaveSpeed(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // miss
	l2 := &mockL2Cache{decisions: nil}                                // miss
	wv := &mockWvClient{
		err: errors.New("weaviate connection refused"), // L3 unavailable
	}
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     true,
			Confidence:   0.88,
			Categories:   map[string]bool{"hate": true},
			ProcessingMs: 180,
		},
	}
	p := newFullPipeline(l1, l2, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should succeed even with L3 down, got: %v", err)
	}

	// Should fall through L3 to WaveSpeed
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE (WaveSpeed after L3 unavailable)", resp.CacheLevel)
	}
	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Decision)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

// TestPipeline_PartialBackPopulationFailure tests: some cache writes fail,
// pipeline continues — cache population is best-effort per spec.
func TestPipeline_PartialBackPopulationFailure(t *testing.T) {
	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{},
		setL1Func: func(_ context.Context, _ string, _ *cache.CachedDecision) error {
			return errors.New("DragonflyDB write timeout")
		},
	}
	l2 := &mockL2Cache{
		decisions: nil,
		setL2Func: func(_ context.Context, _ uint64, _ *cache.CachedDecision) error { return nil },
	}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     true,
			Confidence:   0.91,
			Categories:   map[string]bool{"harassment": true},
			ProcessingMs: 300,
		},
	}
	p := newFullPipeline(l1, l2, a, wv)

	// Should NOT return an error — partial failure is logged, not fatal
	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should succeed even with partial back-population failure, got: %v", err)
	}
	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK (decision returned despite cache write failures)", resp.Decision)
	}
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE", resp.CacheLevel)
	}
}
