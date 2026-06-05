// Package analyzer provides the WaveSpeed AI content moderation integration.
// It defines the Analyzer interface (swappable for mock/real implementations)
// and the HTTP client implementation with circuit breaker resilience.
package analyzer

import "context"

// Analyzer submits content to the WaveSpeed AI API and returns a moderation result.
// The interface is designed to be swappable — mock implementations can satisfy it
// without any HTTP or circuit breaker dependencies.
type Analyzer interface {
	// Analyze submits an image or video URL for content moderation.
	// The mimeType determines which WaveSpeed model endpoint is used
	// (image-content-moderator vs video-content-moderator).
	Analyze(ctx context.Context, imageURL string, mimeType string) (*ModerationResult, error)
}

// ModerationResult holds the structured output from the WaveSpeed API.
// The Categories map uses the WaveSpeed JSON field names as keys
// (harassment, hate, sexual, sexual/minors, violence).
type ModerationResult struct {
	// Decision is true if ANY category is flagged (content should be BLOCKED).
	Decision bool

	// Confidence is derived from the WaveSpeed inference timing metrics.
	// Range 0.0–1.0. Higher confidence = more certain the model is.
	Confidence float64

	// Categories is the raw per-category boolean output from WaveSpeed.
	// Keys: "harassment", "hate", "sexual", "sexual/minors", "violence".
	Categories map[string]bool

	// ProcessingMs is the WaveSpeed inference time in milliseconds.
	ProcessingMs int64
}
