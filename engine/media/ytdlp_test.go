package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestYtDlpRunner_MockSuccess verifies the YtDlpRunner interface contract:
// successful fetch returns bytes and nil error.
func TestYtDlpRunner_MockSuccess(t *testing.T) {
	expected := []byte("yt-dlp-json-output")
	runner := &mockYtDlpRunner{
		output: expected,
	}

	ctx := t.Context()
	got, err := runner.Fetch(ctx, "https://example.com/video")

	if err != nil {
		t.Fatalf("Fetch() unexpected error: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Errorf("Fetch() = %q, want %q", got, expected)
	}
}

// TestYtDlpRunner_MockError verifies error propagation through YtDlpRunner.
func TestYtDlpRunner_MockError(t *testing.T) {
	sentinel := errors.New("yt-dlp: download failed")
	runner := &mockYtDlpRunner{
		err: sentinel,
	}

	ctx := t.Context()
	_, err := runner.Fetch(ctx, "https://example.com/broken")

	if err == nil {
		t.Fatal("Fetch() expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Fetch() error = %v, want %v", err, sentinel)
	}
}

// TestNsJailYtDlp_RedirectLimit verifies redirects are limited to 5
// in the nsjail-wrapper arguments.
func TestNsJailYtDlp_RedirectLimit(t *testing.T) {
	runner := NewNsJailYtDlp("/usr/bin/nsjail", "http://yt-dlp:8080", true)

	// Check that redirect limit is configured
	if runner.maxRedirects != 5 {
		t.Errorf("maxRedirects = %d, want 5", runner.maxRedirects)
	}
}

// TestNsJailYtDlp_DisabledGate verifies that when MEDIA_SANDBOX_ENABLED=false,
// the runner is disabled.
func TestNsJailYtDlp_DisabledGate(t *testing.T) {
	runner := NewNsJailYtDlp("/usr/bin/nsjail", "http://yt-dlp:8080", false)

	if runner.enabled {
		t.Error("sandbox should be disabled when MEDIA_SANDBOX_ENABLED is false")
	}
}

// TestNsJailYtDlp_SidecarURL verifies the yt-dlp sidecar URL is stored.
func TestNsJailYtDlp_SidecarURL(t *testing.T) {
	sidecarURL := "http://yt-dlp-sidecar:9999"
	runner := NewNsJailYtDlp("/usr/bin/nsjail", sidecarURL, true)

	if runner.sidecarURL != sidecarURL {
		t.Errorf("sidecarURL = %q, want %q", runner.sidecarURL, sidecarURL)
	}
}

// TestNsJailYtDlpBuildArgs_NetNone verifies net is disabled in nsjail args.
func TestNsJailYtDlpBuildArgs_NetNone(t *testing.T) {
	args := buildYtDlpNsJailArgs("/usr/bin/nsjail", "http://sidecar:8080", "https://example.com")

	foundNet := false
	for i, a := range args {
		if a == "--net" && i+1 < len(args) && args[i+1] == "none" {
			foundNet = true
		}
	}
	if !foundNet {
		t.Errorf("yt-dlp nsjail args missing --net none: got %v", args)
	}
}

// TestNsJailYtDlp_SidecarHTTPTransport verifies the HTTP transport
// sends requests to the yt-dlp sidecar URL.
func TestNsJailYtDlp_SidecarHTTPTransport(t *testing.T) {
	// Create a test HTTP server that acts as the yt-dlp sidecar.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"title":"test video"}`)
	}))
	defer server.Close()

	// Build the sidecar URL from the test server
	sidecarURL := server.URL
	runner := NewNsJailYtDlp("/usr/bin/nsjail", sidecarURL, true)

	// Make the HTTP request via the transport method
	resp, err := runner.fetchFromSidecar(t.Context(), "https://example.com/video")
	if err != nil {
		t.Fatalf("fetchFromSidecar() error: %v", err)
	}
	if resp == nil {
		t.Fatal("fetchFromSidecar() returned nil response")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// mockYtDlpRunner implements YtDlpRunner for testing.
type mockYtDlpRunner struct {
	output []byte
	err    error
}

func (m *mockYtDlpRunner) Fetch(_ context.Context, _ string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.output, nil
}
