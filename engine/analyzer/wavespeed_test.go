package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestWaveSpeedClient_SatisfiesAnalyzer verifies the WaveSpeedClient
// satisfies the Analyzer interface at compile time.
func TestWaveSpeedClient_SatisfiesAnalyzer(t *testing.T) {
	var _ Analyzer = (*WaveSpeedClient)(nil)
}

// TestWaveSpeedClient_Success verifies the full happy path:
// POST submits → get task ID → poll GET → parse result.
func TestWaveSpeedClient_Success(t *testing.T) {
	ts := waveSpeedMockServer(t, "completed",
		map[string]bool{"harassment": true, "hate": false, "sexual": false, "sexual/minors": false, "violence": false},
		142, // inference ms
	)
	defer ts.Close()

	client := NewWaveSpeedClient(ts.URL, "test-api-key")
	result, err := client.Analyze(t.Context(), "https://example.com/img.jpg", "image/jpeg")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.Decision {
		t.Errorf("Decision = %v, want true (harassment flagged)", result.Decision)
	}
	if result.Confidence <= 0 {
		t.Errorf("Confidence should be > 0, got %f", result.Confidence)
	}
	if result.Categories["harassment"] != true {
		t.Errorf("Categories[harassment] = %v, want true", result.Categories["harassment"])
	}
	if result.Categories["hate"] != false {
		t.Errorf("Categories[hate] = %v, want false", result.Categories["hate"])
	}
	if result.ProcessingMs != 142 {
		t.Errorf("ProcessingMs = %d, want 142", result.ProcessingMs)
	}
}

// TestWaveSpeedClient_CleanContent verifies that when ALL categories
// are false, Decision is false (ALLOW).
func TestWaveSpeedClient_CleanContent(t *testing.T) {
	ts := waveSpeedMockServer(t, "completed",
		map[string]bool{"harassment": false, "hate": false, "sexual": false, "sexual/minors": false, "violence": false},
		95,
	)
	defer ts.Close()

	client := NewWaveSpeedClient(ts.URL, "test-api-key")
	result, err := client.Analyze(t.Context(), "https://example.com/clean.jpg", "image/jpeg")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if result.Decision {
		t.Errorf("Decision = %v, want false (all categories clean)", result.Decision)
	}
	if result.Confidence <= 0 {
		t.Errorf("Confidence should be > 0, got %f", result.Confidence)
	}
	if result.ProcessingMs != 95 {
		t.Errorf("ProcessingMs = %d, want 95", result.ProcessingMs)
	}
}

// TestWaveSpeedClient_VideoMimeType verifies video MIME types use the
// video-content-moderator endpoint.
func TestWaveSpeedClient_VideoMimeType(t *testing.T) {
	var endpointHit string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "video-content-moderator"):
			endpointHit = "video"
			writeJSON(w, http.StatusOK, wavespeedSubmitResponse{
				Code:    200,
				Message: "success",
				Data:    wavespeedData{ID: "task-video", Status: "processing"},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/result"):
			writeJSON(w, http.StatusOK, wavespeedResultResponse{
				Code:    200,
				Message: "success",
				Data: wavespeedData{
					ID:     "task-video",
					Status: "completed",
					Outputs: outputsJSON(map[string]bool{
						"harassment": false, "hate": false, "sexual": false,
						"sexual/minors": false, "violence": true,
					}),
					Timings: &wavespeedTimings{Inference: 250},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := NewWaveSpeedClient(ts.URL, "test-api-key")
	result, err := client.Analyze(t.Context(), "https://example.com/vid.mp4", "video/mp4")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if endpointHit != "video" {
		t.Errorf("Expected video endpoint, got %q", endpointHit)
	}
	if !result.Decision {
		t.Error("Expected Decision=true for violence flag")
	}
	if !result.Categories["violence"] {
		t.Error("Expected violence category to be flagged")
	}
}

// TestWaveSpeedClient_SubmissionError verifies handling of API submission
// errors (non-200, or code != 0).
func TestWaveSpeedClient_SubmissionError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusInternalServerError, wavespeedSubmitResponse{
			Code:    500,
			Message: "internal server error",
		})
	}))
	defer ts.Close()

	client := NewWaveSpeedClient(ts.URL, "test-api-key")
	_, err := client.Analyze(t.Context(), "https://example.com/img.jpg", "image/jpeg")
	if err == nil {
		t.Fatal("Expected error on submission failure, got nil")
	}
}

// TestWaveSpeedClient_PollingTimeout verifies that the Analyze call
// returns an error when the context deadline expires during polling.
// Uses a short context deadline to avoid long test runs.
func TestWaveSpeedClient_PollingTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusOK, wavespeedSubmitResponse{
				Code:    200,
				Message: "success",
				Data:    wavespeedData{ID: "task-stuck", Status: "processing"},
			})
			return
		}
		// Poll returns processing forever
		writeJSON(w, http.StatusOK, wavespeedResultResponse{
			Code:    200,
			Message: "success",
			Data:    wavespeedData{ID: "task-stuck", Status: "processing"},
		})
	}))
	defer ts.Close()

	client := NewWaveSpeedClient(ts.URL, "test-api-key")
	// Use a deadline short enough to trigger quickly, but long enough
	// to survive the initial submission + one poll cycle
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(3*time.Second))
	defer cancel()

	_, err := client.Analyze(ctx, "https://example.com/img.jpg", "image/jpeg")
	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "cancel") && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("Expected deadline/cancel/timeout-related error, got: %v", err)
	}
}

// TestWaveSpeedClient_TaskFailed verifies handling when the task status
// transitions to "failed" during polling.
func TestWaveSpeedClient_TaskFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusOK, wavespeedSubmitResponse{
				Code:    200,
				Message: "success",
				Data:    wavespeedData{ID: "task-fail", Status: "processing"},
			})
			return
		}
		// Poll returns failed
		writeJSON(w, http.StatusOK, wavespeedResultResponse{
			Code:    200,
			Message: "success",
			Data: wavespeedData{
				ID:     "task-fail",
				Status: "failed",
				Error:  "model inference timeout",
			},
		})
	}))
	defer ts.Close()

	client := NewWaveSpeedClient(ts.URL, "test-api-key")
	_, err := client.Analyze(t.Context(), "https://example.com/img.jpg", "image/jpeg")
	if err == nil {
		t.Fatal("Expected error on task failure, got nil")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("Error should mention failed status, got: %v", err)
	}
}

// TestWaveSpeedClient_ConfidenceFromTimings verifies confidence is
// derived from the inference timing metric.
func TestWaveSpeedClient_ConfidenceFromTimings(t *testing.T) {
	tests := []struct {
		name        string
		inferenceMs int64
		wantMinConf float64
		wantMaxConf float64
	}{
		{
			name:        "fast inference = high confidence",
			inferenceMs: 50,
			wantMinConf: 0.9,
			wantMaxConf: 1.0,
		},
		{
			name:        "slow inference = lower confidence",
			inferenceMs: 5000,
			wantMinConf: 0.0,
			wantMaxConf: 0.7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := waveSpeedMockServer(t, "completed",
				map[string]bool{"violence": true, "harassment": false, "hate": false, "sexual": false, "sexual/minors": false},
				tt.inferenceMs,
			)
			defer ts.Close()

			client := NewWaveSpeedClient(ts.URL, "test-api-key")
			result, err := client.Analyze(t.Context(), "https://example.com/img.jpg", "image/jpeg")
			if err != nil {
				t.Fatalf("Analyze error: %v", err)
			}
			if result.Confidence < tt.wantMinConf || result.Confidence > tt.wantMaxConf {
				t.Errorf("Confidence = %f, want in [%f, %f] (inference=%dms)",
					result.Confidence, tt.wantMinConf, tt.wantMaxConf, tt.inferenceMs)
			}
		})
	}
}

// TestWaveSpeedClient_AuthHeader verifies the Authorization header is set.
func TestWaveSpeedClient_AuthHeader(t *testing.T) {
	var authHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusOK, wavespeedSubmitResponse{
				Code:    200,
				Message: "success",
				Data:    wavespeedData{ID: "task-auth", Status: "processing"},
			})
			return
		}
		writeJSON(w, http.StatusOK, wavespeedResultResponse{
			Code:    200,
			Message: "success",
			Data: wavespeedData{
				ID:     "task-auth",
				Status: "completed",
				Outputs: outputsJSON(map[string]bool{
					"harassment": false, "hate": false, "sexual": false,
					"sexual/minors": false, "violence": false,
				}),
				Timings: &wavespeedTimings{Inference: 50},
			},
		})
	}))
	defer ts.Close()

	client := NewWaveSpeedClient(ts.URL, "my-secret-key")
	_, err := client.Analyze(t.Context(), "https://example.com/img.jpg", "image/jpeg")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if authHeader != "Bearer my-secret-key" {
		t.Errorf("Authorization header = %q, want \"Bearer my-secret-key\"", authHeader)
	}
}

// --- helpers ---

// waveSpeedMockServer creates an httptest server that simulates the WaveSpeed
// API submit + poll flow.
func waveSpeedMockServer(t *testing.T, status string, outputs map[string]bool, inferenceMs int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			writeJSON(w, http.StatusOK, wavespeedSubmitResponse{
				Code:    200,
				Message: "success",
				Data:    wavespeedData{ID: "task-123", Status: "processing"},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/result"):
			writeJSON(w, http.StatusOK, wavespeedResultResponse{
				Code:    200,
				Message: "success",
				Data: wavespeedData{
					ID:      "task-123",
					Status:  status,
					Outputs: outputsJSON(outputs),
					Timings: &wavespeedTimings{Inference: inferenceMs},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// writeJSON marshals v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// outputsJSON marshals a category map into json.RawMessage for use in
// wavespeedData.Outputs (which is json.RawMessage, not map[string]bool).
func outputsJSON(m map[string]bool) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("outputsJSON: marshal: %v", err))
	}
	return json.RawMessage(b)
}
