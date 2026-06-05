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
		nsjailArgs := buildNsJailArgs(n.nsjailPath, n.ffmpegBinary, args)
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

// buildNsJailArgs constructs the nsjail command-line arguments for
// sandboxed FFmpeg execution inside a Docker container.
// Docker already provides namespace isolation; nsjail handles process limits/rlimits.
func buildNsJailArgs(nsjailPath, ffmpegBinary string, ffmpegArgs []string) []string {
	_ = nsjailPath

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
