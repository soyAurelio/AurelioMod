package hasher

import (
	"testing"
)

func TestValidateContentType_MatchingType(t *testing.T) {
	// JPEG magic bytes with image/jpeg Content-Type → accepted
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	err := ValidateContentType(data, "image/jpeg")
	if err != nil {
		t.Errorf("ValidateContentType(JPEG, image/jpeg): unexpected error: %v", err)
	}
}

func TestValidateContentType_GenericType(t *testing.T) {
	// JPEG magic bytes with application/octet-stream → accepted (not rejected, just warned)
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	err := ValidateContentType(data, "application/octet-stream")
	if err != nil {
		t.Errorf("ValidateContentType(JPEG, octet-stream): unexpected error: %v", err)
	}
}

func TestValidateContentType_ContradictoryType(t *testing.T) {
	// PE executable magic bytes (MZ) with image/jpeg → rejected
	data := []byte{0x4D, 0x5A, 0x90, 0x00} // MZ header
	err := ValidateContentType(data, "image/jpeg")
	if err == nil {
		t.Fatal("ValidateContentType(PE, image/jpeg): expected error, got nil")
	}
}

func TestValidateContentType_MismatchedType(t *testing.T) {
	// JPEG magic bytes with video/mp4 → rejected
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	err := ValidateContentType(data, "video/mp4")
	if err == nil {
		t.Fatal("ValidateContentType(JPEG, video/mp4): expected error, got nil")
	}
}

func TestValidateContentType_EmptyBody(t *testing.T) {
	// Zero-length body with any Content-Type → rejected
	err := ValidateContentType([]byte{}, "image/jpeg")
	if err == nil {
		t.Fatal("ValidateContentType(empty, image/jpeg): expected error, got nil")
	}
}

func TestDetectMIME_Exported(t *testing.T) {
	// verify the exported detectMIME still works correctly
	tests := []struct {
		name     string
		data     []byte
		expected string
	}{
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg"},
		{"PNG", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{"GIF", []byte{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}, "image/gif"},
		{"WebP", []byte{'R', 'I', 'F', 'F', 0x00, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P'}, "image/webp"},
		{"empty", []byte{}, "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMIME(tt.data)
			if got != tt.expected {
				t.Errorf("DetectMIME() = %q, want %q", got, tt.expected)
			}
		})
	}
}
