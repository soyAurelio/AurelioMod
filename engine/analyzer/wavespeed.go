package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/soyAurelio/AurelioMod/internal/circuitbreaker"
)

// WaveSpeedClient implements the Analyzer interface by calling the
// WaveSpeed AI REST API (Molmo2 content moderator models).
//
// Flow:
//  1. POST submit task with image URL → get task ID
//  2. Poll GET result endpoint every 2s until status="completed" or "failed"
//  3. Parse category booleans → ModerationResult
//
// Circuit breaker: 5 failures/60s → open 30s → half-open (failsafe-go).
// Uses slog for structured request/response logging.
type WaveSpeedClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	executor   failsafe.Executor[*ModerationResult]
}

// Ensure WaveSpeedClient satisfies Analyzer at compile time.
var _ Analyzer = (*WaveSpeedClient)(nil)

// NewWaveSpeedClient creates a WaveSpeed client with circuit breaker
// wrapping all API calls. baseURL is the WaveSpeed API root
// (e.g., "https://api.wavespeed.ai").
func NewWaveSpeedClient(baseURL, apiKey string) *WaveSpeedClient {
	return &WaveSpeedClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		executor: circuitbreaker.WaveSpeedExecutor[*ModerationResult](),
	}
}

// Analyze submits an image/video URL to WaveSpeed for moderation and
// returns the structured moderation result. The entire call (submit + poll)
// is protected by a failsafe-go circuit breaker.
func (c *WaveSpeedClient) Analyze(ctx context.Context, imageURL string, mimeType string) (*ModerationResult, error) {
	return circuitbreaker.Execute(ctx, c.executor, func() (*ModerationResult, error) {
		return c.analyzeWithoutCB(ctx, imageURL, c.endpointForMIME(mimeType))
	})
}

// analyzeWithoutCB performs the submit→poll flow without circuit breaker
// wrapping (the executor handles that externally).
func (c *WaveSpeedClient) analyzeWithoutCB(ctx context.Context, imageURL, endpoint string) (*ModerationResult, error) {
	slog.DebugContext(ctx, "wavespeed: submitting task",
		"endpoint", endpoint,
		"image_url", imageURL,
	)

	// Step 1: Submit task → get task ID
	taskID, err := c.submitTask(ctx, endpoint, imageURL)
	if err != nil {
		slog.ErrorContext(ctx, "wavespeed: submission failed",
			"error", err,
			"endpoint", endpoint,
		)
		return nil, fmt.Errorf("wavespeed submit: %w", err)
	}

	slog.DebugContext(ctx, "wavespeed: task submitted, polling",
		"task_id", taskID,
		"endpoint", endpoint,
	)

	// Step 2: Poll until complete or timeout
	result, err := c.pollResult(ctx, taskID)
	if err != nil {
		slog.ErrorContext(ctx, "wavespeed: polling failed",
			"error", err,
			"task_id", taskID,
		)
		return nil, fmt.Errorf("wavespeed poll: %w", err)
	}

	slog.InfoContext(ctx, "wavespeed: analysis complete",
		"task_id", taskID,
		"decision", result.Decision,
		"confidence", result.Confidence,
		"processing_ms", result.ProcessingMs,
	)

	return result, nil
}

// endpointForMIME selects the correct WaveSpeed API endpoint based on MIME type.
// Image types → image-content-moderator, video types → video-content-moderator.
func (c *WaveSpeedClient) endpointForMIME(mimeType string) string {
	if strings.HasPrefix(mimeType, "video/") {
		return "/api/v3/wavespeed-ai/molmo2/video-content-moderator"
	}
	return "/api/v3/wavespeed-ai/molmo2/image-content-moderator"
}

// --- internal JSON structures ---

// wavespeedSubmitResponse is the submit-task API response envelope.
type wavespeedSubmitResponse struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    wavespeedData `json:"data"`
}

// wavespeedResultResponse is the poll-result API response envelope.
type wavespeedResultResponse struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    wavespeedData `json:"data"`
}

// wavespeedData is the core response body shared by submit and poll.
type wavespeedData struct {
	ID       string             `json:"id"`
	Model    string             `json:"model,omitempty"`
	Outputs  map[string]bool    `json:"outputs,omitempty"`
	Status   string             `json:"status"`
	Error    string             `json:"error,omitempty"`
	Timings  *wavespeedTimings  `json:"timings,omitempty"`
}

// wavespeedTimings holds the inference timing metric.
type wavespeedTimings struct {
	Inference int64 `json:"inference"`
}

// --- API methods ---

// submitTask sends the POST request to create a moderation task.
func (c *WaveSpeedClient) submitTask(ctx context.Context, endpoint, imageURL string) (string, error) {
	body := map[string]any{
		"image": imageURL,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal submit body: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var submit wavespeedSubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&submit); err != nil {
		return "", fmt.Errorf("decode submit response: %w", err)
	}

	if submit.Code != 0 {
		return "", fmt.Errorf("wavespeed API error: code=%d, message=%s", submit.Code, submit.Message)
	}
	if submit.Data.ID == "" {
		return "", fmt.Errorf("wavespeed returned empty task ID")
	}

	return submit.Data.ID, nil
}

// pollResult repeatedly GETs the result endpoint until status is
// "completed" or "failed", or the context expires (max 30s polling window).
func (c *WaveSpeedClient) pollResult(ctx context.Context, taskID string) (*ModerationResult, error) {
	pollInterval := 2 * time.Second
	// The circuit breaker applies its own timeout. A per-context deadline
	// ensures the polling loop itself doesn't exceed 30s.
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Immediate first poll
	result, done, err := c.pollOnce(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if done {
		return result, nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("polling cancelled: %w", ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("polling deadline exceeded after 30s for task %s", taskID)
			}
			result, done, err := c.pollOnce(ctx, taskID)
			if err != nil {
				return nil, err
			}
			if done {
				return result, nil
			}
		}
	}
}

// pollOnce performs a single GET to check task status.
// Returns (nil, false, nil) if still processing.
func (c *WaveSpeedClient) pollOnce(ctx context.Context, taskID string) (*ModerationResult, bool, error) {
	endpoint := fmt.Sprintf("/api/v3/predictions/%s/result", taskID)
	resp, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	var poll wavespeedResultResponse
	if err := json.NewDecoder(resp.Body).Decode(&poll); err != nil {
		return nil, false, fmt.Errorf("decode poll response: %w", err)
	}

	if poll.Code != 0 {
		return nil, false, fmt.Errorf("wavespeed poll error: code=%d, message=%s", poll.Code, poll.Message)
	}

	switch poll.Data.Status {
	case "completed":
		return parseModerationResult(&poll.Data), true, nil
	case "failed":
		errMsg := poll.Data.Error
		if errMsg == "" {
			errMsg = "unknown failure"
		}
		return nil, false, fmt.Errorf("wavespeed task %s failed: %s", taskID, errMsg)
	default:
		// Still processing
		slog.DebugContext(ctx, "wavespeed: task still processing",
			"task_id", taskID,
			"status", poll.Data.Status,
		)
		return nil, false, nil
	}
}

// parseModerationResult converts the WaveSpeed API response into a
// ModerationResult struct. Any non-false category → Decision=true (BLOCKED).
// Confidence is derived from the inference timing metric.
func parseModerationResult(data *wavespeedData) *ModerationResult {
	if data == nil || data.Outputs == nil {
		return &ModerationResult{
			Decision:   false,
			Confidence: 0,
			Categories: map[string]bool{},
		}
	}

	// Derive decision: any true category → block
	decision := false
	for _, flagged := range data.Outputs {
		if flagged {
			decision = true
			break
		}
	}

	// Derive confidence from inference timing.
	// Faster inference → higher confidence. Clamped to [0.05, 0.99].
	confidence := 0.5 // default mid-point
	if data.Timings != nil && data.Timings.Inference > 0 {
		raw := 1.0 - float64(data.Timings.Inference)/10000.0
		confidence = math.Max(0.05, math.Min(0.99, raw))
	}

	processingMs := int64(0)
	if data.Timings != nil {
		processingMs = data.Timings.Inference
	}

	return &ModerationResult{
		Decision:     decision,
		Confidence:   math.Round(confidence*10000) / 10000, // round to 4 decimal places
		Categories:   data.Outputs,
		ProcessingMs: processingMs,
	}
}

// doRequest sends an HTTP request to the WaveSpeed API with auth and JSON headers.
func (c *WaveSpeedClient) doRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s %s: %w", method, url, err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("wavespeed HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return resp, nil
}
