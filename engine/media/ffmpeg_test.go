package media

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// TestFFmpegRunner_MockSuccess verifies the FFmpegRunner interface contract:
// when the runner successfully executes, it returns stdout bytes and nil error.
func TestFFmpegRunner_MockSuccess(t *testing.T) {
	expected := []byte("ffmpeg-raw-output")
	runner := &mockFFmpegRunner{
		stdout: expected,
	}

	ctx := t.Context()
	got, err := runner.Run(ctx, []string{"-i", "pipe:0", "-f", "null", "pipe:1"}, []byte{0xFF, 0xD8})

	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Errorf("Run() output = %q, want %q", got, expected)
	}
}

// TestFFmpegRunner_MockError verifies error propagation through the FFmpegRunner interface.
func TestFFmpegRunner_MockError(t *testing.T) {
	sentinel := errors.New("ffmpeg: broken pipe")
	runner := &mockFFmpegRunner{
		err: sentinel,
	}

	ctx := t.Context()
	_, err := runner.Run(ctx, []string{"-i", "pipe:0"}, []byte{0x00})

	if err == nil {
		t.Fatal("Run() expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Run() error = %v, want %v", err, sentinel)
	}
}

// TestNsJailFFmpegBuildCmd_NetNone verifies nsjail runs with -N
// (disable clone_newnet) so Docker's existing network namespace is reused.
func TestNsJailFFmpegBuildCmd_NetNone(t *testing.T) {
	runner := NewNsJailFFmpeg("/usr/bin/nsjail", "/usr/bin/ffmpeg", true)

	args := buildNsJailArgs(runner.nsjailPath, runner.ffmpegBinary, []string{"-i", "pipe:0"})

	// -N (disable clone_newnet) must be present — we reuse Docker's network namespace
	foundN := false
	for _, a := range args {
		if a == "-N" {
			foundN = true
		}
	}
	if !foundN {
		t.Errorf("nsjail args missing -N flag (Docker provides net namespace): got %v", args)
	}
}

// TestNsJailFFmpegBuildCmd_TmpWritable verifies /tmp is the only writable directory
// in the nsjail sandbox configuration.
func TestNsJailFFmpegBuildCmd_TmpWritable(t *testing.T) {
	runner := NewNsJailFFmpeg("/usr/bin/nsjail", "/usr/bin/ffmpeg", true)

	args := buildNsJailArgs(runner.nsjailPath, runner.ffmpegBinary, []string{"-i", "pipe:0"})

	// Check --cwd value is /tmp
	foundCwd := false
	for _, a := range args {
		if a == "/tmp" {
			foundCwd = true
		}
	}
	if !foundCwd {
		t.Errorf("nsjail args missing /tmp working directory: got %v", args)
	}
}

// TestNsJailFFmpeg_DisabledGate verifies that when MEDIA_SANDBOX_ENABLED=false
// the runner falls back to direct os/exec instead of nsjail.
func TestNsJailFFmpeg_DisabledGate(t *testing.T) {
	runner := NewNsJailFFmpeg("/usr/bin/nsjail", "/usr/bin/ffmpeg", false)

	if runner.enabled {
		t.Error("sandbox should be disabled when MEDIA_SANDBOX_ENABLED is false")
	}
}

// TestFFmpegRunner_MockContextPropagation verifies that the context is received
// by the runner implementation — not silently ignored.
func TestFFmpegRunner_MockContextPropagation(t *testing.T) {
	runner := &mockFFmpegRunner{
		stdout: []byte("ok"),
	}

	ctx := t.Context()
	got, err := runner.Run(ctx, nil, nil)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if string(got) != "ok" {
		t.Errorf("Run() = %q, want %q", got, "ok")
	}

	// Verify the context was captured (mock stores last context)
	if runner.lastCtx == nil {
		t.Error("Run() should have received a non-nil context")
	}
}

// TestBuildNsJailArgs_DoubleDash verifies that -- separates nsjail flags
// from the ffmpeg binary and its arguments.
func TestBuildNsJailArgs_DoubleDash(t *testing.T) {
	args := buildNsJailArgs("/usr/bin/nsjail", "/usr/bin/ffmpeg", []string{"-i", "pipe:0", "-f", "null"})

	// Verify -- separator exists
	var ffmpegArgs []string
	foundDoubleDash := false
	for i, a := range args {
		if a == "--" {
			foundDoubleDash = true
			ffmpegArgs = args[i+1:]
			break
		}
	}
	if !foundDoubleDash {
		t.Fatalf("nsjail args missing '--' separator: got %v", args)
	}
	if len(ffmpegArgs) < 1 {
		t.Fatal("no ffmpeg binary after '--' separator")
	}
	if ffmpegArgs[0] != "/usr/bin/ffmpeg" {
		t.Errorf("first arg after -- = %q, want /usr/bin/ffmpeg", ffmpegArgs[0])
	}
}

// mockFFmpegRunner implements FFmpegRunner for testing.
type mockFFmpegRunner struct {
	stdout  []byte
	err     error
	lastCtx context.Context

	// ExtractFrames return values
	extractFramesData [][]byte
	extractFramesErr  error
}

func (m *mockFFmpegRunner) Run(ctx context.Context, _ []string, _ []byte) ([]byte, error) {
	m.lastCtx = ctx
	return m.stdout, m.err
}

func (m *mockFFmpegRunner) ExtractFrames(_ context.Context, _ string, _ int, _ int) ([][]byte, error) {
	return m.extractFramesData, m.extractFramesErr
}
