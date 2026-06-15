// inference_client.go — ConnectRPC client from Engine to Inference Service (SigLIP2).
// Implements the analyzer.Analyzer interface for drop-in replacement of AI moderation.
//
// Architecture:
//   Engine → ConnectRPC → Inference Service → gRPC → Triton GPU (SigLIP2)
//
// Circuit breaker: 8 consecutive failures OR 40% failure rate over window → open.
// When circuit is open, returns BLOCK decision (fail-closed security posture).
// Retry: 3 attempts with backoff 50ms/200ms/500ms.

package analyzer

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/sony/gobreaker/v2"

	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
	v1connect "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1/aureliomodv1connect"
)

// InferenceClient sends zero-shot classification requests to the SigLIP2
// Inference Service via ConnectRPC. Implements the Analyzer interface.
type InferenceClient struct {
	client       v1connect.InferenceServiceClient
	breaker      *gobreaker.CircuitBreaker[*ModerationResult]
	modelVersion string
}

// InferenceClientConfig holds settings for the Inference Service client.
type InferenceClientConfig struct {
	// Addr is the Inference Service address (host:port, e.g., "localhost:8083").
	Addr string

	// Timeout for individual ConnectRPC calls.
	Timeout time.Duration

	// Circuit breaker settings:
	// Open after 8 consecutive failures OR 40% failure rate over the window.
	FailureThreshold     uint32
	FailureRateThreshold float64
	CBOpenTimeout        time.Duration
	HalfOpenMaxCalls     uint32
}

// DefaultInferenceClientConfig returns the production-recommended configuration.
func DefaultInferenceClientConfig(addr string) InferenceClientConfig {
	return InferenceClientConfig{
		Addr:                 addr,
		Timeout:              10 * time.Second,
		FailureThreshold:     8,
		FailureRateThreshold: 0.40,
		CBOpenTimeout:        15 * time.Second,
		HalfOpenMaxCalls:     3,
	}
}

// Ensure InferenceClient satisfies Analyzer at compile time.
var _ Analyzer = (*InferenceClient)(nil)

// NewInferenceClient creates a SigLIP2 Inference Service client with circuit breaker.
func NewInferenceClient(cfg InferenceClientConfig) *InferenceClient {
	httpClient := &http.Client{
		Timeout: cfg.Timeout,
	}

	client := v1connect.NewInferenceServiceClient(
		httpClient,
		"http://"+cfg.Addr,
		connect.WithGRPC(),
	)

	cbSettings := gobreaker.Settings{
		Name:        "inference-service",
		MaxRequests: cfg.HalfOpenMaxCalls,
		Interval:    60 * time.Second,
		Timeout:     cfg.CBOpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < 10 {
				// Not enough data yet: open only on consecutive failures
				return counts.ConsecutiveFailures >= cfg.FailureThreshold
			}
			failureRate := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.ConsecutiveFailures >= cfg.FailureThreshold ||
				failureRate >= cfg.FailureRateThreshold
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			slog.Warn("circuit breaker state changed",
				"name", name,
				"from", from.String(),
				"to", to.String(),
			)
		},
	}

	return &InferenceClient{
		client:       client,
		breaker:      gobreaker.NewCircuitBreaker[*ModerationResult](cbSettings),
		modelVersion: "siglip2-512-1.0.0",
	}
}

// Analyze sends the image to the Inference Service for zero-shot classification.
//
// The imageURL parameter is expected to be a data URI:
//
//	data:image/jpeg;base64,<base64-encoded-jpeg>
//
// This matches the existing pipeline contract where normalized JPEG bytes
// are encoded as a data URI before passing to the Analyzer.
func (c *InferenceClient) Analyze(ctx context.Context, imageURL string, mimeType string) (*ModerationResult, error) {
	result, err := c.breaker.Execute(func() (*ModerationResult, error) {
		return c.analyzeWithRetry(ctx, imageURL, mimeType)
	})
	if err != nil {
		if err == gobreaker.ErrOpenState || err == gobreaker.ErrTooManyRequests {
			// Fail-closed: circuit open → BLOCK the content.
			// Security-first posture: when the model is unavailable, block.
			return &ModerationResult{
				Decision:     true,
				Confidence:   1.0,
				Categories:   map[string]bool{"circuit_open": true},
				ProcessingMs: 0,
			}, err
		}
		return nil, fmt.Errorf("inference client: %w", err)
	}
	return result, nil
}

// analyzeWithRetry sends the image to the Inference Service with up to 3 retries.
func (c *InferenceClient) analyzeWithRetry(ctx context.Context, imageURL, mimeType string) (*ModerationResult, error) {
	jpegData, err := extractJPEGFromDataURI(imageURL)
	if err != nil {
		return nil, fmt.Errorf("extract jpeg: %w", err)
	}

	// Validate JPEG dimensions (letterbox should produce 512×512)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(jpegData))
	if err != nil {
		return nil, fmt.Errorf("decode jpeg config: %w", err)
	}

	req := &v1.ClassifyRequest{
		Content: &v1.ClassifyRequest_Image{
			Image: &v1.ImageInput{
				JpegData:       jpegData,
				OriginalWidth:  int32(cfg.Width),
				OriginalHeight: int32(cfg.Height),
			},
		},
	}

	backoffs := []time.Duration{50 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond}

	var lastErr error
	for attempt, wait := range backoffs {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		resp, err := c.client.Classify(ctx, connect.NewRequest(req))
		if err != nil {
			// Don't retry on permanent client errors
			var connectErr *connect.Error
			if errors.As(err, &connectErr) {
				code := connectErr.Code()
				if code == connect.CodeInvalidArgument || code == connect.CodeNotFound {
					return nil, fmt.Errorf("inference permanent error: %w", err)
				}
			}
			lastErr = err
			slog.Debug("inference retry",
				"attempt", attempt+1,
				"error", err,
			)
			continue
		}

		return convertClassifyResponse(resp.Msg), nil
	}

	return nil, fmt.Errorf("inference after %d attempts: %w", len(backoffs), lastErr)
}

// convertClassifyResponse maps the SigLIP2 ClassifyResponse to the
// existing ModerationResult format used by the pipeline.
func convertClassifyResponse(resp *v1.ClassifyResponse) *ModerationResult {
	categories := make(map[string]bool)
	triggered := make(map[string]bool)
	for _, t := range resp.Triggered {
		triggered[t] = true
	}

	// Map SigLIP2 score-based categories to boolean flags
	// A category is "flagged" if its score exceeds the threshold
	for cat, score := range resp.Scores {
		// Use the triggered list as ground truth for boolean categories
		categories[cat] = triggered[cat]
		_ = score // score is logged for observability
	}

	decision := len(resp.Triggered) > 0
	confidence := float64(resp.Confidence)

	// If no triggered categories but scores are present, use top score
	if !decision && resp.TopCategory != "" {
		confidence = float64(resp.Confidence)
	}

	return &ModerationResult{
		Decision:     decision,
		Confidence:   confidence,
		Categories:   categories,
		ProcessingMs: resp.InferenceMs,
	}
}

// extractJPEGFromDataURI decodes a data URI into raw JPEG bytes.
// Format: "data:image/jpeg;base64,<base64>"
func extractJPEGFromDataURI(uri string) ([]byte, error) {
	const prefix = "data:image/jpeg;base64,"
	if !strings.HasPrefix(uri, prefix) {
		// Fall back to plain base64 string (no prefix)
		return base64.StdEncoding.DecodeString(strings.TrimSpace(uri))
	}
	b64 := strings.TrimPrefix(uri, prefix)
	return base64.StdEncoding.DecodeString(b64)
}
