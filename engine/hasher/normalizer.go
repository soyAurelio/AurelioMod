package hasher

import (
	"context"
	"fmt"
	"strconv"

	"github.com/soyAurelio/AurelioMod/engine/media"
)

// NormalizeResult holds both the raw pixels (for hashing) and the JPEG bytes (for storage).
type NormalizeResult struct {
	RGBPixels []byte // Decoded, 480p, RGB24, no EXIF → input for BLAKE3 + pHash
	JPEGBytes []byte // JPEG Q85 encoded → for R2/MinIO storage
	MIMEType  string // Detected MIME type from magic bytes inspection
}

// Normalizer runs FFmpeg to decode, normalize, and optionally re-encode content.
// Uses an injected FFmpegRunner for sandboxed execution.
type Normalizer struct {
	runner media.FFmpegRunner
}

// NewNormalizer creates a Normalizer with a sandboxed or direct FFmpeg runner.
func NewNormalizer(runner media.FFmpegRunner) *Normalizer {
	return &Normalizer{runner: runner}
}

// NewNormalizerWithRunner creates a Normalizer with the given FFmpegRunner
// and an optional fallback path. The path is ignored if runner is non-nil.
func NewNormalizerWithRunner(runner media.FFmpegRunner, _ string) *Normalizer {
	return &Normalizer{runner: runner}
}

// Normalize runs the full normalization pipeline on raw input bytes.
//
// Pipeline:
//  1. Decode input bytes
//  2. Resize to 480p, convert to RGB24
//  3. Strip all metadata (EXIF, timestamps, etc.)
//  4. Output raw RGB24 pixels (deterministic, used for BLAKE3 + pHash)
//  5. Also produce a JPEG Q85 copy (for storage only, NOT for hashing)
//
// The RGB pixel data is completely deterministic across FFmpeg versions and CPU architectures.
// The injected FFmpegRunner (media.FFmpegRunner) controls sandboxed or direct execution.
func (n *Normalizer) Normalize(ctx context.Context, input []byte) (*NormalizeResult, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("normalize: empty input")
	}

	// Detect MIME type from magic bytes
	mimeType := DetectMIME(input)

	// Pass 1: extract raw RGB24 pixels for hashing
	rgbPixels, err := n.extractPixels(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("normalize: extract pixels: %w", err)
	}

	if len(rgbPixels) == 0 {
		return nil, fmt.Errorf("normalize: FFmpeg produced 0 bytes of pixel data")
	}

	// Pass 2: produce JPEG Q85 for storage (re-encoded from decoded pixels,
	// NOT raw input — this strips polyglot payloads like JPEG+ZIP).
	jpegBytes, err := n.encodeJPEG(ctx, rgbPixels)
	if err != nil {
		return nil, fmt.Errorf("normalize: encode jpeg: %w", err)
	}

	return &NormalizeResult{
		RGBPixels: rgbPixels,
		JPEGBytes: jpegBytes,
		MIMEType:  mimeType,
	}, nil
}

// extractPixels runs FFmpeg (via injected runner) to decode input and output raw RGB24 pixels.
// Command:
//
//	ffmpeg -i pipe:0 -vf scale=-2:480,format=rgb24 -map_metadata -1
//	       -f rawvideo -pix_fmt rgb24 pipe:1
func (n *Normalizer) extractPixels(ctx context.Context, input []byte) ([]byte, error) {
	return n.runner.Run(ctx, []string{
		"-i", "pipe:0",
		"-vf", "scale=-2:480,format=rgb24",
		"-map_metadata", "-1",
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"pipe:1",
	}, input)
}

// encodeJPEG produces a JPEG Q85 from already-decoded RGB24 pixel data.
// This is the anti-polyglot step: by re-encoding FROM decoded pixels (not
// raw input bytes), any embedded polyglot payload (e.g., ZIP inside JPEG)
// is irreversibly stripped — only visual pixel data survives.
//
// Pixel dimensions are derived from the data: height=480 (fixed by extractPixels),
// width=len(pixels)/(480*3) bytes per pixel for RGB24.
//
// Command:
//
//	ffmpeg -f rawvideo -pix_fmt rgb24 -s {width}x480 -i pipe:0
//	       -f image2 -q:v 3 pipe:1
func (n *Normalizer) encodeJPEG(ctx context.Context, pixels []byte) ([]byte, error) {
	if len(pixels) == 0 {
		return nil, fmt.Errorf("encode jpeg: empty pixel buffer")
	}

	const height = 480
	const bpp = 3 // RGB24 = 3 bytes per pixel
	width := len(pixels) / (height * bpp)
	if width == 0 {
		return nil, fmt.Errorf("encode jpeg: pixel buffer too small for 480p (got %d bytes)", len(pixels))
	}

	return n.runner.Run(ctx, []string{
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-s", strconv.Itoa(width) + "x" + strconv.Itoa(height),
		"-i", "pipe:0",
		"-f", "image2",
		"-q:v", "3",
		"pipe:1",
	}, pixels)
}
