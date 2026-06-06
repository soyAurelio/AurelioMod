package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// mockExec creates a YtdlpFunc that returns the given output and error.
// Simulates yt-dlp CLI execution without spawning a real process.
func mockExec(output string, err error) YtdlpFunc {
	return func(urlStr string, timeout time.Duration) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		return []byte(output), nil
	}
}

// --- test helpers ---

func testRequest(t *testing.T, handler http.Handler, rawURL string) *httptest.ResponseRecorder {
	t.Helper()

	encoded := url.QueryEscape(rawURL)
	req := httptest.NewRequest(http.MethodGet, "/?url="+encoded, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func mustParseBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response body: %v", err)
	}
	return body
}

// --- RED tests — will fail until handler is implemented ---

// TestYtdlpSidecar_ValidURL_Returns200 verifies that a valid YouTube URL
// produces a 200 JSON response with yt-dlp metadata (spec R2.1, S1).
func TestYtdlpSidecar_ValidURL_Returns200(t *testing.T) {
	sampleJSON := `{"title":"Test Video","duration":120,"thumbnail":"https://example.com/thumb.jpg","formats":[]}`

	handler := newSidecarHandler(
		mockExec(sampleJSON, nil),
		true, // gate enabled
	)

	rec := testRequest(t, handler, "https://youtube.com/watch?v=abc123")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	body := mustParseBody(t, rec)
	if body["title"] != "Test Video" {
		t.Errorf("title = %v, want 'Test Video'", body["title"])
	}
}

// TestYtdlpSidecar_YtdlpCrash_Returns502 verifies that when yt-dlp fails,
// the sidecar returns a 502 with a JSON error body (spec R2.5, S2).
func TestYtdlpSidecar_YtdlpCrash_Returns502(t *testing.T) {
	handler := newSidecarHandler(
		mockExec("", errors.New("yt-dlp: command not found")),
		true,
	)

	rec := testRequest(t, handler, "https://youtube.com/watch?v=malformed")

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}

	body := mustParseBody(t, rec)
	if body["error"] == nil || body["error"] == "" {
		t.Error("expected error field in JSON body on yt-dlp failure")
	}
}

// TestYtdlpSidecar_GateDisabled_Returns503 verifies that when
// YTDLP_SIDECAR_ENABLED is false, the handler returns 503 (spec R2.2, S3).
func TestYtdlpSidecar_GateDisabled_Returns503(t *testing.T) {
	handler := newSidecarHandler(
		mockExec("", nil),
		false, // gate disabled
	)

	rec := testRequest(t, handler, "https://youtube.com/watch?v=abc123")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}

	body := mustParseBody(t, rec)
	if body["error"] == nil || body["error"] == "" {
		t.Error("expected error field in JSON body when gate is disabled")
	}
}

// TestYtdlpSidecar_MissingURLParam_Returns400 verifies that a request
// without the `url` query parameter returns a 400 error.
func TestYtdlpSidecar_MissingURLParam_Returns400(t *testing.T) {
	handler := newSidecarHandler(
		mockExec("", nil),
		true,
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing url param)", rec.Code)
	}

	body := mustParseBody(t, rec)
	if body["error"] == nil || body["error"] == "" {
		t.Error("expected error field for missing url param")
	}
}

// TestYtdlpSidecar_EmptyURLParam_Returns400 verifies that an empty url
// parameter produces a 400 error.
func TestYtdlpSidecar_EmptyURLParam_Returns400(t *testing.T) {
	handler := newSidecarHandler(
		mockExec("", nil),
		true,
	)

	req := httptest.NewRequest(http.MethodGet, "/?url=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (empty url param)", rec.Code)
	}
}

// TestYtdlpSidecar_ContentTypeJSON verifies that responses have
// Content-Type: application/json header.
func TestYtdlpSidecar_ContentTypeJSON(t *testing.T) {
	sampleJSON := `{"title":"Test","duration":60}`

	handler := newSidecarHandler(
		mockExec(sampleJSON, nil),
		true,
	)

	rec := testRequest(t, handler, "https://youtube.com/watch?v=abc123")

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// TestYtdlpSidecar_Timeout_Returns502 verifies that the 30s timeout
// on yt-dlp execution produces a 502 error.
func TestYtdlpSidecar_Timeout_Returns502(t *testing.T) {
	// custom exec that blocks, simulating a hung yt-dlp process.
	// The handler should enforce its own 30s timeout via context.
	blockingExec := func(urlStr string, timeout time.Duration) ([]byte, error) {
		return nil, fmt.Errorf("yt-dlp execution timed out after %v", timeout)
	}

	handler := newSidecarHandler(
		blockingExec,
		true,
	)

	rec := testRequest(t, handler, "https://youtube.com/watch?v=slowvideo")

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (timeout)", rec.Code)
	}

	body := mustParseBody(t, rec)
	if body["error"] == nil || body["error"] == "" {
		t.Error("expected error field on timeout")
	}
}
