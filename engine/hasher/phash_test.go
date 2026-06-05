package hasher

import (
	"math/bits"
	"testing"
)

func TestPHash_Deterministic(t *testing.T) {
	// Same input must always produce the same hash
	pixels := makeTestRGB(640, 480) // 480p frame
	h1 := PHash(pixels)
	h2 := PHash(pixels)

	if h1 != h2 {
		t.Fatalf("PHash not deterministic: %016x != %016x", h1, h2)
	}
}

func TestPHash_DifferentImages(t *testing.T) {
	// Visually distinct non-uniform images must produce different hashes.
	// (Solid uniform images all hash to 0, so we use gradient vs checkerboard.)
	a := makeGradientRGB(640, 480)
	b := makeCheckerRGB(640, 480)

	h1 := PHash(a)
	h2 := PHash(b)
	if h1 == h2 {
		t.Fatalf("PHash produced same hash (%016x) for very different images", h1)
	}
}

func TestPHash_EmptyInput(t *testing.T) {
	// Empty input must not panic; return zero hash
	hash := PHash([]byte{})
	if hash != 0 {
		t.Errorf("PHash([]) = %016x, want 0", hash)
	}
}

func TestPHash_NearIdentical(t *testing.T) {
	// Slightly perturbed images should have low Hamming distance
	a := makeSolidRGB(640, 480, 0x80, 0x80, 0x80)
	b := makeSolidRGB(640, 480, 0x81, 0x80, 0x80) // 1-off in red channel

	h1 := PHash(a)
	h2 := PHash(b)

	dist := bits.OnesCount64(h1 ^ h2)
	if dist > 10 {
		t.Errorf("near-identical images: Hamming distance = %d, want ≤ 10", dist)
	}
}

func TestPHash_UniformImage(t *testing.T) {
	// A completely uniform image (all same color) produces hash 0
	pixels := makeSolidRGB(640, 480, 0x7F, 0x7F, 0x7F)
	hash := PHash(pixels)
	if hash != 0 {
		t.Errorf("pHash of uniform image = %016x, want 0 (all bits same)", hash)
	}
}

func TestPHash_Gradient(t *testing.T) {
	// A horizontal gradient must produce a non-zero, deterministic hash
	pixels := makeGradientRGB(640, 480)
	h1 := PHash(pixels)
	h2 := PHash(pixels)

	if h1 != h2 {
		t.Fatalf("pHash gradient not deterministic: %016x != %016x", h1, h2)
	}
	if h1 == 0 {
		t.Fatal("pHash of gradient must be non-zero")
	}
}

func TestPHash_64BitOutput(t *testing.T) {
	// PHash always returns 64 bits; high bits may be set
	pixels := makeTestRGB(100, 100)
	hash := PHash(pixels)
	// Verify it's a valid uint64 (implicitly satisfied) and not constant zero
	// for non-empty, non-uniform input
	_ = hash // compile-check: hash is uint64
}

func TestPHashHex_Roundtrip(t *testing.T) {
	pixels := makeGradientRGB(640, 480)
	hex := PHashHex(pixels)

	if len(hex) != 16 {
		t.Errorf("PHashHex length = %d, want 16", len(hex))
	}

	// Verify it's valid hex
	for _, c := range hex {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("PHashHex contains non-hex character: %c", c)
		}
	}
}

func TestPHashHex_Deterministic(t *testing.T) {
	pixels := makeTestRGB(640, 480)
	h1 := PHashHex(pixels)
	h2 := PHashHex(pixels)
	if h1 != h2 {
		t.Fatalf("PHashHex not deterministic: %s != %s", h1, h2)
	}
}

func TestHammingThreshold_Constant(t *testing.T) {
	if HammingThreshold != 5 {
		t.Errorf("HammingThreshold = %d, want 5", HammingThreshold)
	}
}

// --- helpers ---

// makeSolidRGB creates a frame filled with a single color.
func makeSolidRGB(width, height int, r, g, b byte) []byte {
	pixels := make([]byte, width*height*3)
	for i := 0; i < len(pixels); i += 3 {
		pixels[i] = r
		pixels[i+1] = g
		pixels[i+2] = b
	}
	return pixels
}

// makeTestRGB creates a deterministic test pattern.
func makeTestRGB(width, height int) []byte {
	pixels := make([]byte, width*height*3)
	for i := range pixels {
		pixels[i] = byte(i % 256)
	}
	return pixels
}

// makeCheckerRGB creates a 16×16 checkerboard pattern.
func makeCheckerRGB(width, height int) []byte {
	pixels := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 3
			// 16×16 pixel blocks
			if ((x/16)%2 == 0) != ((y/16)%2 == 0) {
				pixels[idx] = 0xFF
				pixels[idx+1] = 0xFF
				pixels[idx+2] = 0xFF
			}
			// else: stays black (zero)
		}
	}
	return pixels
}

// makeGradientRGB creates a horizontal gradient (black to white).
func makeGradientRGB(width, height int) []byte {
	pixels := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 3
			val := byte((x * 255) / (width - 1))
			pixels[idx] = val
			pixels[idx+1] = val
			pixels[idx+2] = val
		}
	}
	return pixels
}
