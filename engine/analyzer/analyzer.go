// Package analyzer provides content moderation analyzer integrations.
// It defines the Analyzer interface (swappable for mock/real implementations)
// with one implementation:
//   - InferenceClient: SigLIP2 self-hosted zero-shot classifier
package analyzer

import "context"

// Analyzer submits content for AI-powered moderation and returns a result.
// The interface is designed to be swappable — mock implementations can satisfy it
// without any HTTP or circuit breaker dependencies.
type Analyzer interface {
	// Analyze submits an image or video URL for content moderation.
	Analyze(ctx context.Context, imageURL string, mimeType string) (*ModerationResult, error)
}

// ModerationResult holds the structured output from the AI moderation model.
type ModerationResult struct {
	// Decision is true if ANY category is flagged (content should be BLOCKED).
	Decision bool

	// Confidence is derived from model inference confidence.
	// Range 0.0–1.0. Higher confidence = more certain the model is.
	Confidence float64

	// Categories is the raw per-category boolean output from the model.
	// Keys: "harassment", "hate", "sexual", "sexual_minors", "violence".
	Categories map[string]bool

	// ProcessingMs is the model inference time in milliseconds.
	ProcessingMs int64
}
