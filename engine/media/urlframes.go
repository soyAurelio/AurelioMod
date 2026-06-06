package media

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// ParseTimestamp extracts the t= parameter from a YouTube or similar URL.
// Returns the timestamp in seconds and true if a valid t= value was found.
// Supports raw seconds (t=120) and Go duration strings (t=1m30s, t=2h5m).
// Returns (0, false) when no timestamp is present or the value is malformed.
//
// Domain-agnostic: parses any URL containing a t= query parameter,
// not restricted to YouTube. YouTube domain detection is done at the
// pipeline level (spec R3.1).
func ParseTimestamp(rawURL string) (int, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, false
	}

	raw := u.Query().Get("t")
	if raw == "" {
		return 0, false
	}

	// Try parsing as a Go duration first (e.g., 1m30s, 2h5m, 90s, 5m, 1h)
	// Go's time.ParseDuration handles: ns, us/µs, ms, s, m, h combinations.
	if d, err := time.ParseDuration(raw); err == nil {
		secs := int(d.Seconds())
		if secs >= 0 {
			return secs, true
		}
		return 0, false
	}

	// Fall back to raw integer seconds (e.g., t=120)
	if secs, err := strconv.Atoi(raw); err == nil && secs >= 0 {
		return secs, true
	}

	return 0, false
}

// FrameExtractionResult holds extracted frame bytes and metadata.
type FrameExtractionResult struct {
	Frames    [][]byte // PNG-encoded frame bytes
	Timestamp int      // seconds the extraction started from
}

// DownloadAndExtractFrames downloads a video from a YouTube URL to a temp file,
// then extracts up to maxFrames PNG frames via FFmpeg starting at timestampSec.
// The temp file is cleaned up before returning.
//
// Uses yt-dlp CLI to download the video (best quality, merged format).
// FFmpeg extracts frames at 1-second intervals from timestampSec.
// Spec R3.3: 3 frames at -ss {timestamp}, spec R3.6: partial results on failure.
func DownloadAndExtractFrames(ctx context.Context, videoURL string, timestampSec, maxFrames int, ffmpeg FFmpegRunner) (*FrameExtractionResult, error) {
	// Create temp directory for download
	dir, err := os.MkdirTemp("", "aurelio-frames-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir) // best-effort cleanup

	outputPath := filepath.Join(dir, "video.%(ext)s")

	// Download video via yt-dlp with context timeout
	// -f best: best single-file format (no separate audio/video streams)
	// --no-playlist: don't download entire playlists
	// --max-filesize 500M: safety limit
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"-f", "best",
		"-o", outputPath,
		"--no-playlist",
		"--max-filesize", "500M",
		videoURL,
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("yt-dlp download: %w\noutput: %s", err, string(out))
	}

	// Find the downloaded file (yt-dlp may add extension based on format)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read temp dir: %w", err)
	}

	var downloadedPath string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) != ".part" {
			downloadedPath = filepath.Join(dir, e.Name())
			break
		}
	}
	if downloadedPath == "" {
		return nil, fmt.Errorf("no downloaded file found in %s", dir)
	}

	// Extract frames via FFmpeg (spec R3.3)
	frames, err := ffmpeg.ExtractFrames(ctx, downloadedPath, timestampSec, maxFrames)
	if err != nil && len(frames) == 0 {
		return nil, fmt.Errorf("extract frames: %w", err)
	}

	// Spec R3.6: return partial results on partial failure
	return &FrameExtractionResult{
		Frames:    frames,
		Timestamp: timestampSec,
	}, nil
}
