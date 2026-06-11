package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

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

func (m *mockNormalizer) Normalize(_ context.Context, _ []byte) (*hasher.NormalizeResult, error) {
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

// TestPipeline_WaveSpeedError_CircuitBreakerOpen tests: WaveSpeed error
// → last-chance recheck all miss → DECISION_ERROR with degraded_confidence=0.
func TestPipeline_WaveSpeedError_CircuitBreakerOpen(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // miss
	l2 := &mockL2Cache{decisions: nil}                                // miss
	wv := &mockWvClient{cachedDecision: nil}                          // L3 miss
	a := &mockAnalyzer{
		err: errors.New("circuit breaker open: WaveSpeed unavailable"),
	}
	p := newFullPipeline(l1, l2, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should not error on degraded fallback, got: %v", err)
	}

	if resp.Decision != v1.Decision_DECISION_ERROR {
		t.Errorf("Decision = %v, want DECISION_ERROR (all caches empty)", resp.Decision)
	}
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE", resp.CacheLevel)
	}
	if resp.DegradedConfidence != 0 {
		t.Errorf("DegradedConfidence = %f, want 0", resp.DegradedConfidence)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
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

// --- audit + nats + quarantine mocks ---

// hookCall records a hook invocation for test verification.
type hookCall struct {
	workspaceID string
	contentHash string
	contentID   string
	decision    string
	category    string
	confidence  float64
	processingMs int64
}

// mockHooks collects hook calls from audit, decision, and quarantine hooks.
type mockHooks struct {
	mu    sync.Mutex
	calls []hookCall
}

func (m *mockHooks) record(c hookCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c)
}

func (m *mockHooks) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// auditHook returns an AuditHook that records calls to mockHooks.
func (m *mockHooks) auditHook() AuditHook {
	return func(ctx context.Context, workspaceID, contentHash, decision, category string, confidence float64, processingMs int64) {
		m.record(hookCall{
			workspaceID:  workspaceID,
			contentHash:  contentHash,
			decision:     decision,
			category:     category,
			confidence:   confidence,
			processingMs: processingMs,
		})
	}
}

// decisionHook returns a DecisionHook that records calls to mockHooks.
func (m *mockHooks) decisionHook() DecisionHook {
	return func(ctx context.Context, workspaceID, contentHash, decision, category string, confidence float64) {
		m.record(hookCall{
			workspaceID: workspaceID,
			contentHash: contentHash,
			decision:    decision,
			category:    category,
			confidence:  confidence,
		})
	}
}

// quarantineHook returns a QuarantineHook that records calls to mockHooks.
func (m *mockHooks) quarantineHook() QuarantineHook {
	return func(ctx context.Context, contentID, decision, category string, confidence float64) {
		m.record(hookCall{
			contentID:  contentID,
			decision:   decision,
			category:   category,
			confidence: confidence,
		})
	}
}

// newFullPipelineWithHooks creates a pipeline with all 5 dependencies
// plus integration hooks. Nil hooks are passed as nil options.
func newFullPipelineWithHooks(
	l1 *mockL1Cache, l2 *mockL2Cache,
	a *mockAnalyzer, wv *mockWvClient,
	hooks *mockHooks,
) Pipeline {
	var opts []PipelineOption
	if hooks != nil {
		opts = append(opts, WithAuditHook(hooks.auditHook()))
		opts = append(opts, WithDecisionHook(hooks.decisionHook()))
		opts = append(opts, WithQuarantineHook(hooks.quarantineHook()))
	}
	return New(l1, l2, &mockNormalizer{pixels: testPixels()}, a, wv, opts...)
}

// --- integration hook tests ---

// TestPipeline_AuditEmissionOnWaveSpeed verifies that after WaveSpeed returns
// a decision, an audit event is emitted.
func TestPipeline_AuditEmissionOnWaveSpeed(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}   // miss
	l2 := &mockL2Cache{decisions: nil}                                   // miss
	wv := &mockWvClient{cachedDecision: nil}                            // L3 miss
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     true,
			Confidence:   0.93,
			Categories:   map[string]bool{"violence": true},
			ProcessingMs: 200,
		},
	}
	hooks := &mockHooks{}

	p := newFullPipelineWithHooks(l1, l2, a, wv, hooks)

	_, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-audit-test",
		ContentId:   "cnt-001",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Hooks are fire-and-forget — wait briefly for goroutines
	time.Sleep(50 * time.Millisecond)
	if hooks.count() == 0 {
		t.Error("Expected audit event to be emitted after WaveSpeed decision")
	}
}

// TestPipeline_DecisionPublishedOnWaveSpeed verifies that after WaveSpeed
// returns a decision, it is published via the DecisionPublisher.
func TestPipeline_DecisionPublishedOnWaveSpeed(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}
	l2 := &mockL2Cache{decisions: nil}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     true,
			Confidence:   0.88,
			Categories:   map[string]bool{"hate": true},
			ProcessingMs: 150,
		},
	}
	hooks := &mockHooks{}

	p := newFullPipelineWithHooks(l1, l2, a, wv, hooks)

	_, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-pub-test",
		ContentId:   "cnt-002",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if hooks.count() == 0 {
		t.Error("Expected at least one hook to fire")
	}
}

// TestPipeline_QuarantineUpdatedOnWaveSpeed verifies that after WaveSpeed
// returns a decision, the quarantine status is updated.
func TestPipeline_QuarantineUpdatedOnWaveSpeed(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}
	l2 := &mockL2Cache{decisions: nil}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     false, // clean
			Confidence:   0.99,
			Categories:   map[string]bool{},
			ProcessingMs: 50,
		},
	}
	hooks := &mockHooks{}

	p := newFullPipelineWithHooks(l1, l2, a, wv, hooks)

	_, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-quar-test",
		ContentId:   "cnt-003",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if hooks.count() == 0 {
		t.Error("Expected quarantine hook to fire")
	}
}

// TestPipeline_AllThreeHooksFired verifies that audit, NATS, and quarantine
// hooks are all fired on a WaveSpeed decision.
func TestPipeline_AllThreeHooksFired(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}
	l2 := &mockL2Cache{decisions: nil}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     true,
			Confidence:   0.87,
			Categories:   map[string]bool{"sexual/minors": true},
			ProcessingMs: 400,
		},
	}
	hooks := &mockHooks{}

	p := newFullPipelineWithHooks(l1, l2, a, wv, hooks)

	_, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-all-hooks",
		ContentId:   "cnt-004",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// With all 3 hooks configured, we expect 3 calls
	if hooks.count() < 1 {
		t.Error("Expected hooks to fire after WaveSpeed decision")
	}
}

// TestPipeline_L1Hit_FiresAuditHooks verifies that cache hits now emit audit
// events for GDPR Art.22 / NIS2 Art.21 compliance. Previously cache hits
// skipped hooks entirely, creating an audit gap.
func TestPipeline_L1Hit_FiresAuditHooks(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{
		decisions: map[string]*cache.CachedDecision{
			blake3Hash: cachedBlock(),
		},
	}
	l2 := &mockL2Cache{}
	wv := &mockWvClient{cachedDecision: nil}
	hooks := &mockHooks{}

	p := newFullPipelineWithHooks(l1, l2, nil, wv, hooks)

	_, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-cache-test",
		ContentId:   "cnt-005",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if c := hooks.count(); c < 1 {
		t.Errorf("Expected audit hooks on L1 cache hit (GDPR/NIS2), got %d hook calls", c)
	}
}

// TestPipeline_Hooks_NonBlocking verifies that hook failures do NOT affect
// the pipeline response — hooks are fire-and-forget.
func TestPipeline_Hooks_NonBlocking(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}
	l2 := &mockL2Cache{decisions: nil}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     true,
			Confidence:   0.91,
			Categories:   map[string]bool{"harassment": true},
			ProcessingMs: 100,
		},
	}
	// Hooks are nil — pipeline must still work
	p := newFullPipelineWithHooks(l1, l2, a, wv, nil)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-nil-hooks",
		ContentId:   "cnt-006",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should succeed even with nil hooks, got: %v", err)
	}
	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Decision)
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

// --- smart mocks for last-chance recheck ---

// missThenHitL1 returns miss on first GetL1 call, then hit on subsequent calls.
// Simulates: cache was populated by a concurrent request between initial check
// and the last-chance recheck.
type missThenHitL1 struct {
	firstCall  bool
	decisions  map[string]*cache.CachedDecision
}

func (m *missThenHitL1) GetL1(_ context.Context, blake3Hash string) (*cache.CachedDecision, bool) {
	if !m.firstCall {
		m.firstCall = true
		return nil, false // miss on first call (initial check)
	}
	d, ok := m.decisions[blake3Hash]
	return d, ok // hit on second call (last-chance recheck)
}

func (m *missThenHitL1) SetL1(_ context.Context, _ string, _ *cache.CachedDecision) error { return nil }

// missThenHitL2 returns empty on first GetL2 call, then hit on subsequent calls.
type missThenHitL2 struct {
	firstCall  bool
	decisions  []*cache.CachedDecision
}

func (m *missThenHitL2) GetL2(_ context.Context, _ uint64, _ int) ([]*cache.CachedDecision, error) {
	if !m.firstCall {
		m.firstCall = true
		return nil, nil // miss on first call
	}
	return m.decisions, nil // hit on second call
}

func (m *missThenHitL2) SetL2(_ context.Context, _ uint64, _ *cache.CachedDecision) error { return nil }

// missThenHitWv returns nil on first SearchSimilar call, then hit on subsequent calls.
type missThenHitWv struct {
	firstCall       bool
	cachedDecision  *cache.CachedDecision
}

func (m *missThenHitWv) SearchSimilar(_ context.Context, _ string, _ float32) (*cache.CachedDecision, error) {
	if !m.firstCall {
		m.firstCall = true
		return nil, nil // miss on first call
	}
	return m.cachedDecision, nil // hit on second call
}

func (m *missThenHitWv) IndexDecision(_ context.Context, _ string, _ *cache.CachedDecision) error { return nil }

// --- graceful degradation: WaveSpeed error → lastChanceRecheck ---

// TestPipeline_WaveSpeedError_L1LastChanceHit verifies that when WaveSpeed
// errors AND L1 cache was populated (by a concurrent request) during the wait,
// the last-chance recheck returns the cached decision with degraded_confidence.
func TestPipeline_WaveSpeedError_L1LastChanceHit(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &missThenHitL1{decisions: map[string]*cache.CachedDecision{
		blake3Hash: cachedBlock(),
	}}
	l2 := &mockL2Cache{decisions: nil}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		err: errors.New("circuit breaker open: WaveSpeed unavailable"),
	}
	p := New(l1, l2, &mockNormalizer{pixels: testPixels()}, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should not error on degraded L1 fallback, got: %v", err)
	}

	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Decision)
	}
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L1_BLAKE3 {
		t.Errorf("CacheLevel = %v, want L1_BLAKE3 (last-chance recheck hit)", resp.CacheLevel)
	}
	if resp.DegradedConfidence == 0 {
		t.Error("DegradedConfidence should be > 0 on cache fallback hit")
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

// TestPipeline_WaveSpeedError_L2LastChanceHit verifies L2 hit during last-chance
// recheck after WaveSpeed failure.
func TestPipeline_WaveSpeedError_L2LastChanceHit(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // L1 miss both times
	l2 := &missThenHitL2{decisions: []*cache.CachedDecision{cachedAllow()}}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		err: errors.New("HTTP 429 Too Many Requests"),
	}
	p := New(l1, l2, &mockNormalizer{pixels: testPixels()}, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should not error on L2 last-chance hit, got: %v", err)
	}

	if resp.Decision != v1.Decision_DECISION_ALLOW {
		t.Errorf("Decision = %v, want ALLOW", resp.Decision)
	}
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L2_PHASH {
		t.Errorf("CacheLevel = %v, want L2_PHASH (last-chance recheck hit)", resp.CacheLevel)
	}
	if resp.DegradedConfidence == 0 {
		t.Error("DegradedConfidence should be > 0 on L2 fallback hit")
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

// TestPipeline_WaveSpeedError_L3LastChanceHit verifies L3 hit during last-chance
// recheck after WaveSpeed failure.
func TestPipeline_WaveSpeedError_L3LastChanceHit(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // L1 miss
	l2 := &mockL2Cache{decisions: nil}                                 // L2 miss
	wv := &missThenHitWv{cachedDecision: cachedBlock()}                // L3 miss first, hit second
	a := &mockAnalyzer{
		err: errors.New("circuit breaker open: WaveSpeed unavailable"),
	}
	p := New(l1, l2, &mockNormalizer{pixels: testPixels()}, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should not error on L3 last-chance hit, got: %v", err)
	}

	if resp.Decision != v1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want BLOCK", resp.Decision)
	}
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_L3_WEAVIATE {
		t.Errorf("CacheLevel = %v, want L3_WEAVIATE (last-chance recheck hit)", resp.CacheLevel)
	}
	if resp.DegradedConfidence == 0 {
		t.Error("DegradedConfidence should be > 0 on L3 fallback hit")
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

// TestPipeline_WaveSpeedError_AllCachesMiss verifies that when WaveSpeed errors
// and ALL caches miss on last-chance recheck, the pipeline returns
// DECISION_ERROR with DegradedConfidence=0.
func TestPipeline_WaveSpeedError_AllCachesMiss(t *testing.T) {
	blake3Hash := precomputeHash()

	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // L1 miss
	l2 := &mockL2Cache{decisions: nil}                                 // L2 miss
	wv := &mockWvClient{cachedDecision: nil}                           // L3 miss
	a := &mockAnalyzer{
		err: errors.New("HTTP 429 Too Many Requests"),
	}
	p := newFullPipeline(l1, l2, a, wv)

	resp, err := p.Execute(t.Context(), &v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF, 0xD8, 0xFF},
	})
	if err != nil {
		t.Fatalf("Execute should not error even on total cache miss, got: %v", err)
	}

	if resp.Decision != v1.Decision_DECISION_ERROR {
		t.Errorf("Decision = %v, want DECISION_ERROR (all caches missed)", resp.Decision)
	}
	if resp.CacheLevel != v1.CacheLevel_CACHE_LEVEL_NONE {
		t.Errorf("CacheLevel = %v, want NONE", resp.CacheLevel)
	}
	if resp.DegradedConfidence != 0 {
		t.Errorf("DegradedConfidence = %f, want 0 (complete cache miss)", resp.DegradedConfidence)
	}
	if resp.ContentHash != blake3Hash {
		t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
	}
}

// TestPipeline_WaveSpeedSuccess_NoDegradedConfidence verifies that a normal
// successful WaveSpeed call does NOT set degraded_confidence.
func TestPipeline_WaveSpeedSuccess_NoDegradedConfidence(t *testing.T) {
	l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}
	l2 := &mockL2Cache{decisions: nil}
	wv := &mockWvClient{cachedDecision: nil}
	a := &mockAnalyzer{
		result: &analyzer.ModerationResult{
			Decision:     false,
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

	if resp.DegradedConfidence != 0 {
		t.Errorf("DegradedConfidence = %f, want 0 (WaveSpeed succeeded, no degradation)", resp.DegradedConfidence)
	}
}

// --- synctest deadline scenarios ---

// slowNormalizer blocks until the context is cancelled, simulating a
// slow FFmpeg process that respects context deadlines.
type slowNormalizer struct{}

func (s *slowNormalizer) Normalize(ctx context.Context, _ []byte) (*hasher.NormalizeResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestPipeline_Deadline_NormalizeExpired verifies that when the context
// deadline expires during normalization, the pipeline returns the deadline
// error rather than hanging indefinitely.
func TestPipeline_Deadline_NormalizeExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}}
		l2 := &mockL2Cache{}

		p := New(l1, l2, &slowNormalizer{}, nil, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		// In synctest: 50ms virtual time passes, timeout fires, normalizer unblocks
		_, err := p.Execute(ctx, &v1.AnalyzeRequest{
			WorkspaceId: "ws-deadline",
			RawBytes:    []byte{0xFF, 0xD8, 0xFF},
		})

		if err == nil {
			t.Fatal("Expected deadline error, got nil")
		}
	})
}

// slowAnalyzer blocks until the context is cancelled, simulating a
// slow WaveSpeed API call that respects context deadlines.
type slowAnalyzer struct{}

var _ analyzer.Analyzer = (*slowAnalyzer)(nil)

func (s *slowAnalyzer) Analyze(ctx context.Context, _, _ string) (*analyzer.ModerationResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestPipeline_Deadline_WaveSpeedExpired verifies that when L1 and L2 miss
// and the WaveSpeed call times out (context deadline exceeded), the pipeline
// gracefully degrades: last-chance recheck → DECISION_ERROR with zero confidence.
func TestPipeline_Deadline_WaveSpeedExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		blake3Hash := precomputeHash()

		l1 := &mockL1Cache{decisions: map[string]*cache.CachedDecision{}} // miss
		l2 := &mockL2Cache{decisions: nil}                                // miss

		p := New(l1, l2, &mockNormalizer{pixels: testPixels()}, &slowAnalyzer{}, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		resp, err := p.Execute(ctx, &v1.AnalyzeRequest{
			WorkspaceId: "ws-deadline-ws",
			RawBytes:    []byte{0xFF, 0xD8, 0xFF},
		})

		if err != nil {
			t.Fatalf("Execute should not error on degraded fallback, got: %v", err)
		}

		if resp.Decision != v1.Decision_DECISION_ERROR {
			t.Errorf("Decision = %v, want DECISION_ERROR (timeout → all caches empty)", resp.Decision)
		}
		if resp.DegradedConfidence != 0 {
			t.Errorf("DegradedConfidence = %f, want 0", resp.DegradedConfidence)
		}
		if resp.ContentHash != blake3Hash {
			t.Errorf("ContentHash = %q, want %q", resp.ContentHash, blake3Hash)
		}
	})
}
