// Package hasher provides BLAKE3 content hashing and pHash perceptual hashing
// for the AurelioMod content moderation pipeline.
//
// Pipeline:
//
//	raw content → FFmpeg decode → resize 480p → strip EXIF
//	  ├── raw RGB24 pixels → BLAKE3 hash (L1 cache key, deterministic)
//	  ├── raw RGB24 pixels → pHash (L2 cache key, perceptual)
//	  └── JPEG Q85 encode → storage (R2/MinIO)
package hasher

import (
	"encoding/hex"

	"lukechampine.com/blake3"
)

// HashL1 computes a BLAKE3 hash over raw RGB24 pixel data for L1 cache lookups.
// The input must be decoded, normalized pixels (480p, no EXIF), NOT encoded bytes.
// This guarantees deterministic hashing across FFmpeg versions and CPU architectures.
//
// Performance: ~3 GB/s per core. At 480p (640×480×3 = ~900KB), <1ms.
func HashL1(rgbPixels []byte) string {
	hash := blake3.Sum256(rgbPixels)
	return hex.EncodeToString(hash[:])
}

// HashL1Bytes returns the raw 32-byte BLAKE3 hash for database storage.
func HashL1Bytes(rgbPixels []byte) [32]byte {
	return blake3.Sum256(rgbPixels)
}

// NormalizationSpec documents the required normalization pipeline before hashing.
// These constants MUST be used in the Engine's FFmpeg invocation.
const (
	// ResizeTarget is the canonical resolution for content normalization.
	ResizeTarget = "scale=-2:480"

	// PixelFormat is the canonical pixel format for deterministic hashing.
	PixelFormat = "rgb24"

	// FFmpegVersion is the pinned FFmpeg version for the Engine container.
	// NEVER use :latest — changing the encoder invalidates cached hashes.
	FFmpegVersion = "jrottenberg/ffmpeg:7.1-ubuntu"

	// JPEGQuality is the quality setting for the storage copy (NOT used for hashing).
	JPEGQuality = "q:v=3" // Q85 equivalent
)
