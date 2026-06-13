package weaviate

import (
	"context"
	"testing"

	"github.com/soyAurelio/AurelioMod/internal/cache"
)

// TestWeaviateClient_InterfaceContract verifies the WeaviateClient interface
// can be satisfied by mock implementations for pipeline testing.
func TestWeaviateClient_InterfaceContract(t *testing.T) {
	var _ WeaviateClient = (*mockClient)(nil)

	mock := &mockClient{
		searchResult: &cache.CachedDecision{
			Decision:   1,
			Confidence: 0.94,
			Category:   "hate_speech",
		},
	}

	result, err := mock.SearchSimilar(t.Context(), testVector(), 0.92)
	if err != nil {
		t.Fatalf("SearchSimilar error: %v", err)
	}
	if result.Decision != 1 {
		t.Errorf("Decision = %d, want 1", result.Decision)
	}
	if result.Confidence != 0.94 {
		t.Errorf("Confidence = %f, want 0.94", result.Confidence)
	}
	if result.Category != "hate_speech" {
		t.Errorf("Category = %q, want hate_speech", result.Category)
	}
}

// TestWeaviateClient_IndexDecision verifies IndexDecision passes through
// correctly on the mock.
func TestWeaviateClient_IndexDecision(t *testing.T) {
	mock := &mockClient{}

	err := mock.IndexDecision(t.Context(), "abc123hash", testVector(), &cache.CachedDecision{
		Decision:   2,
		Confidence: 0.97,
		Category:   "violence_graphic",
	})
	if err != nil {
		t.Fatalf("IndexDecision error: %v", err)
	}

	if mock.lastIndexed == nil {
		t.Fatal("Expected IndexDecision to record the decision")
	}
	if mock.lastIndexed.ContentHash != "abc123hash" {
		t.Errorf("ContentHash = %q, want abc123hash", mock.lastIndexed.ContentHash)
	}
	if mock.lastIndexed.Decision.Decision != 2 {
		t.Errorf("Decision = %d, want 2", mock.lastIndexed.Decision.Decision)
	}
}

// TestWeaviateClient_SearchSimilar_NotFound verifies nil is returned
// when no similar content is found (below threshold).
func TestWeaviateClient_SearchSimilar_NotFound(t *testing.T) {
	mock := &mockClient{
		searchResult: nil, // simulates no match found
	}

	result, err := mock.SearchSimilar(t.Context(), testVector(), 0.92)
	if err != nil {
		t.Fatalf("SearchSimilar error: %v", err)
	}
	if result != nil {
		t.Errorf("Expected nil result for missing content, got %+v", result)
	}
}

// TestWeaviateClient_SearchSimilar_Error verifies errors are propagated.
func TestWeaviateClient_SearchSimilar_Error(t *testing.T) {
	mock := &mockClient{
		searchError: context.DeadlineExceeded,
	}

	_, err := mock.SearchSimilar(t.Context(), nil, 0.92)
	if err == nil {
		t.Fatal("Expected error from SearchSimilar, got nil")
	}
}

// TestWeaviateClient_IndexDecision_Error verifies index errors propagate.
func TestWeaviateClient_IndexDecision_Error(t *testing.T) {
	mock := &mockClient{
		indexError: context.DeadlineExceeded,
	}

	err := mock.IndexDecision(t.Context(), "any-hash", nil, &cache.CachedDecision{})
	if err == nil {
		t.Fatal("Expected error from IndexDecision, got nil")
	}
}

// --- mockClient ---

type indexedDecision struct {
	ContentHash string
	Vector      []float32
	Decision    *cache.CachedDecision
}

// mockClient implements WeaviateClient for testing.
type mockClient struct {
	searchResult *cache.CachedDecision
	searchError  error
	indexError   error
	lastIndexed  *indexedDecision
}

func (m *mockClient) SearchSimilar(_ context.Context, _ []float32, _ float32) (*cache.CachedDecision, error) {
	if m.searchError != nil {
		return nil, m.searchError
	}
	return m.searchResult, nil
}

func (m *mockClient) IndexDecision(_ context.Context, contentHash string, vector []float32, decision *cache.CachedDecision) error {
	if m.indexError != nil {
		return m.indexError
	}
	m.lastIndexed = &indexedDecision{ContentHash: contentHash, Vector: vector, Decision: decision}
	return nil
}

// testVector returns a 64-dim float32 vector for testing.
func testVector() []float32 {
	vec := make([]float32, 64)
	vec[0] = 1.0
	vec[63] = 1.0
	return vec
}
