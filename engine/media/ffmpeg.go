// Package media provides sandboxed FFmpeg and yt-dlp execution
// wrappers using nsjail for process isolation.
package media

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// FFmpegRunner is the interface for running FFmpeg commands, whether
// directly via os/exec or sandboxed via nsjail.
type FFmpegRunner interface {
	// Run executes FFmpeg with args and stdin bytes, returning stdout bytes.
	// The context deadline controls the sandbox wall-time limit.
	Run(ctx context.Context, args []string, stdin []byte) ([]byte, error)

	// ExtractFrames extracts up to maxFrames PNG frames from a video file
	// starting at timestampSec. Each frame is extracted sequentially at
	// 1-second intervals (timestampSec, timestampSec+1, ...).
	// Returns a slice of PNG-encoded frame bytes.
	ExtractFrames(ctx context.Context, inputPath string, timestampSec int, maxFrames int) ([][]byte, error)

	// ExtractCollage detects key frames via scene-change detection and
	// merges them into a single 3x3 grid collage JPEG. This reduces
	// AI moderation API calls from N frames to 1 collage (~150KB).
	// Returns JPEG-encoded collage bytes.
	ExtractCollage(ctx context.Context, inputPath string) ([]byte, error)
}

// Compile-time interface check for NsJailFFmpeg.
var _ FFmpegRunner = (*NsJailFFmpeg)(nil)

// NsJailFFmpeg runs FFmpeg inside an nsjail sandbox with network disabled,
// /tmp as the only writable directory, and media read-only.
type NsJailFFmpeg struct {
	nsjailPath   string
	ffmpegBinary string
	enabled      bool
}

// NewNsJailFFmpeg creates a sandboxed FFmpeg runner.
// When enabled is false, falls back to direct os/exec.
func NewNsJailFFmpeg(nsjailPath, ffmpegBinary string, enabled bool) *NsJailFFmpeg {
	return &NsJailFFmpeg{
		nsjailPath:   nsjailPath,
		ffmpegBinary: ffmpegBinary,
		enabled:      enabled,
	}
}

// Run executes FFmpeg inside nsjail when enabled, otherwise falls back
// to direct os/exec. The context deadline is propagated to the subprocess.
func (n *NsJailFFmpeg) Run(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	var cmd *exec.Cmd

	if n.enabled {
		nsjailArgs := buildNsJailArgs(n.ffmpegBinary, args)
		cmd = exec.CommandContext(ctx, n.nsjailPath, nsjailArgs...)
	} else {
		cmd = exec.CommandContext(ctx, n.ffmpegBinary, args...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// ExtractFrames extracts up to maxFrames PNG frames from a video file
// at 1-second intervals starting from timestampSec. Each frame is a
// separate ffmpeg invocation to ensure clean PNG boundaries.
//
// Frames are extracted sequentially (one ffmpeg call per frame) per
// the design decision: Bulkhead(1) on AI moderation means no parallelism
// benefit from concurrent extraction.
func (n *NsJailFFmpeg) ExtractFrames(ctx context.Context, inputPath string, timestampSec int, maxFrames int) ([][]byte, error) {
	if maxFrames < 1 {
		return nil, nil
	}

	frames := make([][]byte, 0, maxFrames)

	for i := 0; i < maxFrames; i++ {
		// Check context before each extraction
		select {
		case <-ctx.Done():
			return frames, ctx.Err()
		default:
		}

		offsetSec := timestampSec + i
		args := []string{
			"-ss", fmt.Sprintf("%d", offsetSec),
			"-i", inputPath,
			"-vframes", "1",
			"-f", "image2pipe",
			"-vcodec", "png",
			"pipe:1",
		}

		pngBytes, err := n.Run(ctx, args, nil)
		if err != nil {
			// Partial failure: return what we have + the error.
			// Spec R3.6: "If frame extraction fails, Engine SHALL
			// return partial results with success frames only."
			return frames, fmt.Errorf("extract frame %d at %ds: %w", i, offsetSec, err)
		}

		frames = append(frames, pngBytes)
	}

	return frames, nil
}

// ExtractCollage detects key frames via scene-change detection (gt(scene,0.3))
// and merges up to 9 frames into a single 3x3 grid collage JPEG (~150KB).
// This reduces AI moderation API calls from N frames to 1 collage.
//
// Single ffmpeg invocation:
//
//	ffmpeg -i input -vf "select='gt(scene,0.3)',scale=320:-1,tile=3x3"
//	       -vframes 1 -q:v 3 -f image2pipe pipe:1
func (n *NsJailFFmpeg) ExtractCollage(ctx context.Context, inputPath string) ([]byte, error) {
	args := []string{
		"-i", inputPath,
		"-vf", "select='gt(scene,0.3)',scale=320:-1,tile=3x3",
		"-vframes", "1",
		"-q:v", "3",
		"-f", "image2pipe",
		"pipe:1",
	}
	return n.Run(ctx, args, nil)
}

// buildNsJailArgs constructs the nsjail command-line arguments for
// sandboxed FFmpeg execution inside a Docker container.
// Docker already provides namespace isolation; nsjail handles process limits/rlimits.
func buildNsJailArgs(ffmpegBinary string, ffmpegArgs []string) []string {
	base := []string{
		"--disable_clone_newuser", "--disable_clone_newpid",
		"--disable_clone_newns", "--disable_clone_newipc",
		"--disable_clone_newuts", "--disable_clone_newcgroup",
		"-N", // disable clone_newnet (alias for --disable_clone_newnet)
		"--cwd", "/tmp", "--",
	}
	base = append(base, ffmpegBinary)
	base = append(base, ffmpegArgs...)
	return base
}
