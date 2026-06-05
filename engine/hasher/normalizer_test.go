package hasher

import (
	"context"
	"errors"
	"testing"
)

// TestNormalizer_InjectedRunner_CallsRun verifies that when a mock
// FFmpegRunner is injected, Normalize calls Run() instead of os/exec.
func TestNormalizer_InjectedRunner_CallsRun(t *testing.T) {
	// Create a mock runner that returns pre-canned RGB24 pixels for
	// extractPixels and JPEG bytes for encodeJPEG.
	mock := &mockRunner{
		fn: func(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
			// Return different outputs based on output format
			if len(args) > 0 && args[len(args)-1] == "pipe:1" {
				// Check if this is JPEG or rawvideo
				for _, a := range args {
					if a == "image2" {
						return []byte{0xFF, 0xD8, 0xFF, 0xE0}, nil // JPEG header
					}
				}
				return []byte{0x01, 0x02, 0x03}, nil // raw pixels
			}
			return nil, nil
		},
	}

	n := NewNormalizerWithRunner(mock, "")
	ctx := t.Context()

	result, err := n.Normalize(ctx, []byte{0xFF, 0xD8, 0xFF})
	if err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}

	if result == nil {
		t.Fatal("Normalize() returned nil result")
	}
	// Verify the mock runner was called (it provided the output)
	if result.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q, want image/jpeg (from JPEG magic)", result.MIMEType)
	}
	if len(result.RGBPixels) == 0 {
		t.Error("RGBPixels should not be empty")
	}
	if len(result.JPEGBytes) == 0 {
		t.Error("JPEGBytes should not be empty")
	}

	// Verify the mock was called at least twice (extractPixels + encodeJPEG)
	if mock.callCount < 2 {
		t.Errorf("mockRunner called %d times, want at least 2 (extractPixels + encodeJPEG)", mock.callCount)
	}
}

// TestNormalizer_InjectedRunner_ErrorPropagation verifies that when
// the injected runner returns an error, Normalize propagates it.
func TestNormalizer_InjectedRunner_ErrorPropagation(t *testing.T) {
	mock := &mockRunner{
		fn: func(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
			return nil, errors.New("ffmpeg: killed by nsjail")
		},
	}

	n := NewNormalizerWithRunner(mock, "")
	ctx := t.Context()

	_, err := n.Normalize(ctx, []byte{0xFF, 0xD8, 0xFF})
	if err == nil {
		t.Fatal("Normalize() expected error, got nil")
	}
}

// TestNormalizer_ContextPropagation verifies that the context is
// passed through to the FFmpegRunner.
func TestNormalizer_ContextPropagation(t *testing.T) {
	var receivedCtx context.Context
	mock := &mockRunner{
		fn: func(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
			receivedCtx = ctx
			return []byte{0x01, 0x02, 0x03}, nil
		},
	}

	n := NewNormalizerWithRunner(mock, "")
	ctx := t.Context()

	_, err := n.Normalize(ctx, []byte{0xFF, 0xD8, 0xFF})
	if err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}

	if receivedCtx == nil {
		t.Error("FFmpegRunner.Run() should have received context")
	}
}

// mockRunner implements media.FFmpegRunner for testing.
type mockRunner struct {
	fn        func(ctx context.Context, args []string, stdin []byte) ([]byte, error)
	callCount int
}

func (m *mockRunner) Run(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	m.callCount++
	return m.fn(ctx, args, stdin)
}
