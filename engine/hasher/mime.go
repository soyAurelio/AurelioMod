package hasher

import (
	"fmt"
	"log/slog"
)

// ValidateContentType checks that the declared Content-Type matches the
// actual content detected from magic bytes. It implements MIME validation
// as specified in the mime-validation spec.
//
// Rules:
//   - Empty body → reject
//   - Matching type (e.g., JPEG magic + image/jpeg) → accept
//   - Generic type (application/octet-stream) → accept with warning
//   - Contradictory type (PE magic + image/jpeg) → reject
//   - Mismatched type (JPEG magic + video/mp4) → reject
func ValidateContentType(raw []byte, contentType string) error {
	if len(raw) == 0 {
		return fmt.Errorf("MIME validation: body is empty")
	}

	detected := DetectMIME(raw)

	if contentType == "application/octet-stream" {
		// Generic type — accept but warn
		slog.Warn("MIME validation: generic Content-Type received",
			"declared", contentType,
			"detected", detected,
		)
		return nil
	}

	if detected == contentType {
		return nil
	}

	return fmt.Errorf(
		"Content-Type %q contradicts detected MIME %q",
		contentType, detected,
	)
}

// DetectMIME inspects magic bytes to determine the content type.
// This is the canonical MIME detection used throughout the Engine.
func DetectMIME(data []byte) string {
	dlen := len(data)
	if dlen == 0 {
		return "application/octet-stream"
	}

	// JPEG: FF D8 FF (needs 3 bytes)
	if dlen >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}

	// PNG: 89 50 4E 47 (needs 8 bytes for full header check)
	if dlen >= 8 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png"
	}

	// GIF: 47 49 46 38 (needs 6 bytes)
	if dlen >= 6 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x38 {
		return "image/gif"
	}

	// ZIP-based (polyglot detection): PK (needs 4 bytes)
	if dlen >= 4 && data[0] == 'P' && data[1] == 'K' {
		return "application/zip"
	}

	// Remaining formats need at least 12 bytes
	if dlen < 12 {
		return "application/octet-stream"
	}

	// WebP: 52 49 46 46 ... 57 45 42 50
	if data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' &&
		data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		return "image/webp"
	}

	// MP4: ... ftyp at offset 4
	if data[4] == 'f' && data[5] == 't' && data[6] == 'y' && data[7] == 'p' {
		return "video/mp4"
	}

	return "application/octet-stream"
}
