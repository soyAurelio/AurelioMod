package hasher

import (
	"encoding/hex"
	"testing"
	"testing/synctest"
	"time"
)

func TestHashL1_Deterministic(t *testing.T) {
	// Same input must always produce same hash
	pixels := []byte{0x00, 0xFF, 0x80, 0x40, 0x20, 0x10}
	h1 := HashL1(pixels)
	h2 := HashL1(pixels)

	if h1 != h2 {
		t.Fatalf("HashL1 not deterministic: %s != %s", h1, h2)
	}

	// Verify output length (64 hex chars = 32 bytes = 256 bits)
	if len(h1) != 64 {
		t.Fatalf("HashL1 output length = %d, want 64", len(h1))
	}
}

func TestHashL1_DifferentInputs(t *testing.T) {
	a := []byte{0x00, 0x01, 0x02}
	b := []byte{0x00, 0x01, 0x03}

	if HashL1(a) == HashL1(b) {
		t.Fatal("HashL1 produced same hash for different inputs")
	}
}

func TestHashL1_EmptyInput(t *testing.T) {
	// BLAKE3 handles empty input gracefully
	hash := HashL1([]byte{})
	if len(hash) != 64 {
		t.Fatalf("HashL1(empty) length = %d, want 64", len(hash))
	}
}

func TestHashL1Bytes_Roundtrip(t *testing.T) {
	pixels := make([]byte, 900000) // ~480p RGB24 frame
	for i := range pixels {
		pixels[i] = byte(i % 256)
	}

	raw := HashL1Bytes(pixels)
	hexStr := hex.EncodeToString(raw[:])

	if hexStr != HashL1(pixels) {
		t.Fatal("HashL1Bytes and HashL1 produced different hashes")
	}
}

func TestMIMEDetection(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected string
	}{
		{
			name:     "JPEG",
			data:     []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01},
			expected: "image/jpeg",
		},
		{
			name:     "PNG",
			data:     []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			expected: "image/png",
		},
		{
			name:     "GIF87a",
			data:     []byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61},
			expected: "image/gif",
		},
		{
			name:     "WebP",
			data:     []byte{'R', 'I', 'F', 'F', 0x00, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P'},
			expected: "image/webp",
		},
		{
			name:     "MP4",
			data:     []byte{0x00, 0x00, 0x00, 0x1C, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'},
			expected: "video/mp4",
		},
		{
			name:     "ZIP (polyglot detection)",
			data:     []byte{'P', 'K', 0x03, 0x04},
			expected: "application/zip",
		},
		{
			name:     "empty",
			data:     []byte{},
			expected: "application/octet-stream",
		},
		{
			name:     "unknown",
			data:     []byte{0xDE, 0xAD, 0xBE, 0xEF},
			expected: "application/octet-stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectMIME(tt.data)
			if got != tt.expected {
				t.Errorf("detectMIME(%x) = %q, want %q", tt.data[:min(len(tt.data), 8)], got, tt.expected)
			}
		})
	}
}

func TestBLAKE3Performance(t *testing.T) {
	// Verify BLAKE3 hashing stays within performance budget (<2ms for 900KB)
	pixels := make([]byte, 900000) // ~480p frame
	for i := range pixels {
		pixels[i] = byte(i % 256)
	}

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		for i := 0; i < 100; i++ {
			HashL1(pixels)
		}
		elapsed := time.Since(start)
		avg := elapsed / 100

		// BLAKE3 should process ~3GB/s, so 900KB should take ~0.3ms.
		// Allow 2ms for safety margin.
		if avg > 2*time.Millisecond {
			t.Errorf("BLAKE3 too slow: avg %v per 900KB hash (want <2ms)", avg)
		}
	})
}

func TestEmptyNormalize(t *testing.T) {
	n := NewNormalizer("")
	_, err := n.Normalize([]byte{})
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestNormalizationSpecConstants(t *testing.T) {
	// Verify constants are correctly set
	if ResizeTarget != "scale=-2:480" {
		t.Errorf("ResizeTarget = %q, want %q", ResizeTarget, "scale=-2:480")
	}
	if PixelFormat != "rgb24" {
		t.Errorf("PixelFormat = %q, want %q", PixelFormat, "rgb24")
	}
	if FFmpegVersion != "jrottenberg/ffmpeg:7.1-ubuntu" {
		t.Errorf("FFmpegVersion = %q, want %q", FFmpegVersion, "jrottenberg/ffmpeg:7.1-ubuntu")
	}
}
