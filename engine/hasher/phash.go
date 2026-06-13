package hasher

import "fmt"

// HammingThreshold is the canonical maximum Hamming distance for L2 pHash cache hits.
// Derived from the spec: entries with Hamming distance ≤ 5 are considered matches.
const HammingThreshold = 5

// PHash computes a perceptual hash (dHash) from raw RGB24 pixel data.
//
// Algorithm: Difference Hash (dHash)
//  1. Downscale to 9×8 grayscale using BT.601 luminance
//  2. Compare adjacent columns: gray[x] < gray[x+1] → bit = 1
//  3. Result is a 64-bit unsigned integer (8 rows × 8 comparisons)
//
// The input must be decoded, normalized RGB24 pixels at 480p height,
// as produced by Normalizer.Normalize(). The width is inferred from the
// byte count (len / 3 / 480).
//
// Uniform images produce hash = 0. Near-identical images produce low
// Hamming distance from each other.
func PHash(rgbPixels []byte) uint64 {
	if len(rgbPixels) == 0 {
		return 0
	}

	totalPixels := len(rgbPixels) / 3
	if totalPixels == 0 {
		return 0
	}

	// Normalizer always outputs 480p height; infer width from buffer size.
	const inputHeight = 480
	inputWidth := totalPixels / inputHeight
	if inputWidth == 0 {
		return 0
	}

	// Step 1: Downscale to 9×8 grayscale
	const targetW, targetH = 9, 8
	gray := make([]byte, targetW*targetH)

	for y := 0; y < targetH; y++ {
		for x := 0; x < targetW; x++ {
			// Map target pixel to nearest source pixel
			srcX := x * inputWidth / targetW
			srcY := y * inputHeight / targetH
			idx := (srcY*inputWidth + srcX) * 3

			r := int(rgbPixels[idx])
			g := int(rgbPixels[idx+1])
			b := int(rgbPixels[idx+2])

			// BT.601 luminance: Y = 0.299R + 0.587G + 0.114B
			gray[y*targetW+x] = byte((299*r + 587*g + 114*b) / 1000)
		}
	}

	// Step 2: Compute difference hash across adjacent columns
	var hash uint64
	for y := 0; y < targetH; y++ {
		rowOff := y * targetW
		for x := 0; x < targetW-1; x++ {
			if gray[rowOff+x] < gray[rowOff+x+1] {
				hash |= 1 << uint(y*8+x)
			}
		}
	}

	return hash
}

// PHashHex returns the perceptual hash as a 16-character hex string.
// This is the canonical format for Redis L2 cache keys.
func PHashHex(rgbPixels []byte) string {
	h := PHash(rgbPixels)
	return fmt.Sprintf("%016x", h)
}

// PHashVector converts a perceptual hash (uint64) into a 64-dimensional
// float32 vector for Weaviate L3 vector search. Each bit becomes 0.0 or 1.0.
//
// The vector captures the same perceptual fingerprint as PHash, enabling
// Weaviate's HNSW-indexed nearVector queries to find visually similar content
// with cosine similarity > 0.92 — broader than L2's Hamming distance ≤ 5.
func PHashVector(ph uint64) []float32 {
	vec := make([]float32, 64)
	for i := range 64 {
		if ph&(1<<uint(i)) != 0 {
			vec[i] = 1.0
		}
	}
	return vec
}
