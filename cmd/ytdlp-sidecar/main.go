// ytdlp-sidecar is a sandboxed HTTP wrapper around yt-dlp CLI.
// It satisfies the §7 sandboxing requirement: Engine calls the sidecar
// instead of shelling out to yt-dlp directly.
//
// Endpoint: GET /?url=<encoded_youtube_url>
// Response: raw yt-dlp JSON output
//
// Env gates:
//
//	YTDLP_SIDECAR_ENABLED  (default true)   — toggle the HTTP endpoint
//	YTDLP_SIDECAR_PORT     (default 8080)   — listen port
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Env gates
	enabled := envBool("YTDLP_SIDECAR_ENABLED", true)
	port := envString("YTDLP_SIDECAR_PORT", "8080")

	// Real yt-dlp executor: spawns yt-dlp --print-json with a timeout.
	realExec := func(rawURL string, timeout time.Duration) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "yt-dlp",
			"--no-progress",
			"--print-json",
			"--max-redirects", "5",
			rawURL,
		)
		return cmd.Output()
	}

	handler := newSidecarHandler(realExec, enabled)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 35 * time.Second, // 30s yt-dlp timeout + overhead
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("ytdlp-sidecar starting",
			"port", port,
			"enabled", enabled,
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("ytdlp-sidecar listen error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("ytdlp-sidecar shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("ytdlp-sidecar shutdown error", "error", err)
	}
}

// envBool reads a boolean env var with a default fallback.
func envBool(key string, def bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	switch raw {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// envString reads a string env var with a default fallback.
func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
