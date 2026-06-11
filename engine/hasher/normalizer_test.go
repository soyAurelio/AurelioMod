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
	// Pixels must be at least 480*3 = 1440 bytes for the width calculation.
	mock := &mockRunner{
		fn: func(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
			// Check if this is JPEG encoding (image2) — check first
			// because encodeJPEG args contain both "rawvideo" and "image2"
			for _, a := range args {
				if a == "image2" {
					return []byte{0xFF, 0xD8, 0xFF, 0xE0}, nil // JPEG header
				}
			}
			// extractPixels: return enough bytes for 480p (at least 1px width)
			return make([]byte, 480*3), nil // 1x480 RGB24 = 1440 bytes
		},
	}

	n := NewNormalizer(mock)
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

	n := NewNormalizer(mock)
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
			// Both extractPixels and encodeJPEG need valid sizes.
			// Return enough bytes for 1x480 RGB24 = 1440 bytes.
			return make([]byte, 480*3), nil
		},
	}

	n := NewNormalizer(mock)
	ctx := t.Context()

	_, err := n.Normalize(ctx, []byte{0xFF, 0xD8, 0xFF})
	if err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}

	if receivedCtx == nil {
		t.Error("FFmpegRunner.Run() should have received context")
	}
}

// TestNormalizer_JPEGAntiPolyglot verifies the anti-polyglot protection:
// when input contains JPEG + trailing ZIP (polyglot), encodeJPEG receives
// only the decoded RGB24 pixels, not the raw polyglot bytes. This ensures
// the ZIP payload is irreversibly stripped by the decode→pixels→re-encode
// pipeline.
//
// Spec: media-sandbox R4 — Single-Pass JPEG Re-encode
func TestNormalizer_JPEGAntiPolyglot(t *testing.T) {
	// Construct a polyglot: JPEG header + garbage ZIP-like data appended
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	zipPayload := []byte{0x50, 0x4B, 0x03, 0x04} // PK.. ZIP magic
	polyglot := append(append([]byte{}, jpegHeader...), zipPayload...)

	// Expected clean pixel data (simulating what extractPixels would return)
	cleanPixels := make([]byte, 640*480*3) // 640x480 RGB24
	for i := range cleanPixels {
		cleanPixels[i] = byte(i % 256)
	}

	// Expected clean JPEG output (from re-encoding pixels)
	cleanJPEG := []byte{0xFF, 0xD8, 0xFF, 0xDB} // JPEG with DQT marker

	var encodeJPEGStdin []byte

	mock := &mockRunner{
		fn: func(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
			// Determine whether this is extractPixels or encodeJPEG.
			// Check image2 (JPEG) first — encodeJPEG contains both rawvideo AND image2.
			isJPEG := false
			isRawvideo := false
			for _, a := range args {
				if a == "image2" {
					isJPEG = true
				}
				if a == "rawvideo" {
					isRawvideo = true
				}
			}

			if isJPEG {
				// encodeJPEG: capture what it received (should be clean pixels)
				encodeJPEGStdin = make([]byte, len(stdin))
				copy(encodeJPEGStdin, stdin)
				return cleanJPEG, nil
			}
			if isRawvideo {
				// extractPixels: return clean pixel data
				return cleanPixels, nil
			}
			return nil, nil
		},
	}

	n := NewNormalizer(mock)
	ctx := t.Context()

	result, err := n.Normalize(ctx, polyglot)
	if err != nil {
		t.Fatalf("Normalize() error: %v", err)
	}

	// Core assertion: encodeJPEG received pixels, NOT raw polyglot
	if len(encodeJPEGStdin) == 0 {
		t.Fatal("encodeJPEG was not called or received empty stdin")
	}

	// The stdin to encodeJPEG should be the pixel data (921600 bytes for 640x480x3)
	expectedPixelLen := 640 * 480 * 3
	if len(encodeJPEGStdin) != expectedPixelLen {
		t.Errorf("encodeJPEG stdin length = %d, want %d (clean pixel data)", len(encodeJPEGStdin), expectedPixelLen)
	}

	// Verify the ZIP magic is NOT present in what encodeJPEG received
	if containsBytes(encodeJPEGStdin, zipPayload) {
		t.Error("FAIL: ZIP payload found in encodeJPEG stdin — polyglot NOT stripped")
	}

	// Verify output JPEG does not contain ZIP data
	if containsBytes(result.JPEGBytes, zipPayload) {
		t.Error("FAIL: ZIP payload found in output JPEG — polyglot survived")
	}

	// Verify output size: JPEG header only, no ZIP data
	if len(result.JPEGBytes) >= len(cleanJPEG)+len(zipPayload) {
		t.Errorf("JPEGBytes length = %d, want < %d (ZIP removed)", len(result.JPEGBytes), len(cleanJPEG)+len(zipPayload))
	}
}

// containsBytes checks if needle is present in haystack.
func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if bytesEqual(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

// bytesEqual compares two byte slices for equality.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNormalizer_CleanJPEG_NoPolyglot verifies that a clean JPEG (no polyglot)
// still works correctly through the decode→pixels→re-encode pipeline.
func TestNormalizer_CleanJPEG_NoPolyglot(t *testing.T) {
	cleanJPEG := []byte{0xFF, 0xD8, 0xFF, 0xE0} // minimal JPEG header

	cleanPixels := make([]byte, 640*480*3)
	for i := range cleanPixels {
		cleanPixels[i] = byte((i + 42) % 256)
	}
	expectedJPEG := []byte{0xFF, 0xD8, 0xFF, 0xDB}

	var encodeStdinLen int
	mock := &mockRunner{
		fn: func(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
			for _, a := range args {
				if a == "image2" {
					encodeStdinLen = len(stdin)
					return expectedJPEG, nil
				}
			}
			return cleanPixels, nil
		},
	}

	n := NewNormalizer(mock)
	ctx := t.Context()

	result, err := n.Normalize(ctx, cleanJPEG)
	if err != nil {
		t.Fatalf("Normalize() error on clean JPEG: %v", err)
	}

	if result == nil {
		t.Fatal("Normalize() returned nil result")
	}
	if result.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q, want image/jpeg", result.MIMEType)
	}
	if len(result.JPEGBytes) == 0 {
		t.Error("JPEGBytes should not be empty for clean JPEG")
	}

	// Verify encodeJPEG received pixel-sized data (not raw JPEG bytes)
	expectedPixelLen := 640 * 480 * 3
	if encodeStdinLen != expectedPixelLen {
		t.Errorf("encodeJPEG stdin = %d bytes, want %d (pixel data)", encodeStdinLen, expectedPixelLen)
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

func (m *mockRunner) ExtractFrames(_ context.Context, _ string, _ int, _ int) ([][]byte, error) {
	return nil, nil
}

func (m *mockRunner) ExtractCollage(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
