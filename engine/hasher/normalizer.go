package hasher

import (
	"bytes"
	"fmt"
	"os/exec"
)

// NormalizeResult holds both the raw pixels (for hashing) and the JPEG bytes (for storage).
type NormalizeResult struct {
	RGBPixels []byte // Decoded, 480p, RGB24, no EXIF → input for BLAKE3 + pHash
	JPEGBytes []byte // JPEG Q85 encoded → for R2/MinIO storage
	MIMEType  string // Detected MIME type from magic bytes inspection
}

// Normalizer runs FFmpeg to decode, normalize, and optionally re-encode content.
type Normalizer struct {
	ffmpegPath string
}

// NewNormalizer creates a Normalizer. Uses "ffmpeg" if path is empty.
func NewNormalizer(ffmpegPath string) *Normalizer {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &Normalizer{ffmpegPath: ffmpegPath}
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
func (n *Normalizer) Normalize(input []byte) (*NormalizeResult, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("normalize: empty input")
	}

	// Detect MIME type from magic bytes
	mimeType := DetectMIME(input)

	// Pass 1: extract raw RGB24 pixels for hashing
	rgbPixels, err := n.extractPixels(input)
	if err != nil {
		return nil, fmt.Errorf("normalize: extract pixels: %w", err)
	}

	if len(rgbPixels) == 0 {
		return nil, fmt.Errorf("normalize: FFmpeg produced 0 bytes of pixel data")
	}

	// Pass 2: produce JPEG Q85 for storage (separate, not used for hash)
	jpegBytes, err := n.encodeJPEG(input)
	if err != nil {
		return nil, fmt.Errorf("normalize: encode jpeg: %w", err)
	}

	return &NormalizeResult{
		RGBPixels: rgbPixels,
		JPEGBytes: jpegBytes,
		MIMEType:  mimeType,
	}, nil
}

// extractPixels runs FFmpeg to decode input and output raw RGB24 pixels.
// Command:
//
//	ffmpeg -i pipe:0 -vf scale=-2:480,format=rgb24 -map_metadata -1
//	       -f rawvideo -pix_fmt rgb24 pipe:1
func (n *Normalizer) extractPixels(input []byte) ([]byte, error) {
	cmd := exec.Command(n.ffmpegPath,
		"-i", "pipe:0", // Read from stdin
		"-vf", "scale=-2:480,format=rgb24", // Resize + force pixel format
		"-map_metadata", "-1", // Strip all metadata
		"-f", "rawvideo", // Output format: raw video
		"-pix_fmt", "rgb24", // Pixel format
		"pipe:1", // Write to stdout
	)

	return runFFmpegPipe(cmd, input)
}

// encodeJPEG runs FFmpeg to produce a JPEG Q85 from the normalized input.
// Command:
//
//	ffmpeg -i pipe:0 -vf scale=-2:480,format=rgb24 -map_metadata -1
//	       -f image2 -q:v 3 pipe:1
func (n *Normalizer) encodeJPEG(input []byte) ([]byte, error) {
	cmd := exec.Command(n.ffmpegPath,
		"-i", "pipe:0",
		"-vf", "scale=-2:480,format=rgb24",
		"-map_metadata", "-1",
		"-f", "image2", // Output format: single image
		"-q:v", "3", // JPEG quality (2-5 range, 3 ≈ Q85)
		"pipe:1",
	)

	return runFFmpegPipe(cmd, input)
}

// runFFmpegPipe executes an FFmpeg command with stdin/stdout pipe I/O.
func runFFmpegPipe(cmd *exec.Cmd, input []byte) ([]byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg error: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}
