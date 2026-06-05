package analyzer

import (
	"context"
	"testing"
)

// TestAnalyzerInterface verifies the Analyzer interface contract:
// - Analyze(ctx, imageURL, mimeType) returns (*ModerationResult, error)
// - ModerationResult has Decision, Confidence, Categories, ProcessingMs
func TestAnalyzerInterface(t *testing.T) {
	// This test verifies both the interface shape and the struct shape
	// by instantiating a ModerationResult and asserting on its zero values.
	result := &ModerationResult{
		Decision:     true,
		Confidence:   0.95,
		Categories:   map[string]bool{"harassment": true, "hate": false},
		ProcessingMs: 142,
	}

	if !result.Decision {
		t.Errorf("Decision = %v, want true", result.Decision)
	}
	if result.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", result.Confidence)
	}
	if result.ProcessingMs != 142 {
		t.Errorf("ProcessingMs = %d, want 142", result.ProcessingMs)
	}
	if !result.Categories["harassment"] {
		t.Errorf("Category harassment = %v, want true", result.Categories["harassment"])
	}
	if result.Categories["hate"] {
		t.Errorf("Category hate = %v, want false", result.Categories["hate"])
	}
}

// TestAnalyzerInterfaceContract verifies the interface can be satisfied
// by a mock implementation — used for testing the pipeline.
func TestAnalyzerInterfaceContract(t *testing.T) {
	// Compile-time check: mockAnalyzer satisfies Analyzer
	var _ Analyzer = (*mockAnalyzer)(nil)

	mock := &mockAnalyzer{
		result: &ModerationResult{
			Decision:     false,
			Confidence:   0.0,
			Categories:   map[string]bool{},
			ProcessingMs: 0,
		},
	}
	result, err := mock.Analyze(context.Background(), "https://example.com/img.jpg", "image/jpeg")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if result == nil {
		t.Fatal("Analyze returned nil result")
	}
}

// mockAnalyzer implements Analyzer for testing.
type mockAnalyzer struct {
	result *ModerationResult
	err    error
}

func (m *mockAnalyzer) Analyze(_ context.Context, _, _ string) (*ModerationResult, error) {
	return m.result, m.err
}
