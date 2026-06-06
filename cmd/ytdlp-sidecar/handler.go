package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// YtdlpFunc executes yt-dlp for the given URL with a timeout.
// The raw JSON stdout is returned on success; an error is returned
// if the process fails or times out.
type YtdlpFunc func(rawURL string, timeout time.Duration) ([]byte, error)

// newSidecarHandler returns an http.Handler that serves the ytdlp-sidecar
// HTTP API. When enabled is false, all requests return 503.
//
// Endpoint: GET /?url=<encoded_url>
// Response: raw yt-dlp JSON output (200), or {"error":"..."} (502/503/400)
// Timeout:   30s per yt-dlp invocation (spec R2.4)
func newSidecarHandler(exec YtdlpFunc, enabled bool) http.Handler {
	mux := http.NewServeMux()

	// Health check — always available regardless of gate.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Main handler
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Gate check (spec R2.2)
		if !enabled {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeError(w, "ytdlp-sidecar is disabled (YTDLP_SIDECAR_ENABLED=false)")
			return
		}

		// Parse and validate url query param (spec R2.1)
		rawURL := r.URL.Query().Get("url")
		if rawURL == "" {
			w.WriteHeader(http.StatusBadRequest)
			writeError(w, "missing required query parameter: url")
			return
		}

		decodedURL, err := url.QueryUnescape(rawURL)
		if err != nil || decodedURL == "" {
			w.WriteHeader(http.StatusBadRequest)
			writeError(w, "invalid url parameter")
			return
		}

		// Execute yt-dlp with 30s timeout (spec R2.4)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		type result struct {
			output []byte
			err    error
		}
		ch := make(chan result, 1)

		go func() {
			output, err := exec(decodedURL, 30*time.Second)
			ch <- result{output, err}
		}()

		select {
		case res := <-ch:
			if res.err != nil {
				slog.Error("yt-dlp execution failed",
					"url", decodedURL,
					"error", res.err,
				)
				w.WriteHeader(http.StatusBadGateway)
				writeError(w, "yt-dlp execution failed")
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(res.output)

		case <-ctx.Done():
			slog.Error("yt-dlp execution timed out",
				"url", decodedURL,
				"error", ctx.Err(),
			)
			w.WriteHeader(http.StatusBadGateway)
			writeError(w, "yt-dlp execution timed out")
		}
	})

	return mux
}

// writeError writes a JSON error response body.
// Must be called AFTER setting the response status code.
func writeError(w http.ResponseWriter, message string) {
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
