// Integration smoke tests for the edge-discord main binary.
// These tests validate the wiring compiles correctly and that core
// utility functions produce expected results.
package main

import (
	"testing"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

func TestAttContentType_mapping(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		want     aureliomodv1.ContentType
	}{
		{"jpeg image", "image/jpeg", aureliomodv1.ContentType_CONTENT_TYPE_IMAGE},
		{"png image", "image/png", aureliomodv1.ContentType_CONTENT_TYPE_IMAGE},
		{"gif image", "image/gif", aureliomodv1.ContentType_CONTENT_TYPE_IMAGE},
		{"webp image", "image/webp", aureliomodv1.ContentType_CONTENT_TYPE_IMAGE},
		{"mp4 video", "video/mp4", aureliomodv1.ContentType_CONTENT_TYPE_VIDEO},
		{"webm video", "video/webm", aureliomodv1.ContentType_CONTENT_TYPE_VIDEO},
		{"quicktime video", "video/quicktime", aureliomodv1.ContentType_CONTENT_TYPE_VIDEO},
		{"mp3 audio", "audio/mpeg", aureliomodv1.ContentType_CONTENT_TYPE_AUDIO},
		{"ogg audio", "audio/ogg", aureliomodv1.ContentType_CONTENT_TYPE_AUDIO},
		{"wav audio", "audio/wav", aureliomodv1.ContentType_CONTENT_TYPE_AUDIO},
		{"unknown type", "application/pdf", aureliomodv1.ContentType_CONTENT_TYPE_EXTERNAL_URL},
		{"text file", "text/plain", aureliomodv1.ContentType_CONTENT_TYPE_EXTERNAL_URL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attContentType(tt.mimeType)
			if got != tt.want {
				t.Errorf("attContentType(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestIsImageType(t *testing.T) {
	if !isImageType("image/jpeg") {
		t.Error("image/jpeg should be recognized as image")
	}
	if !isImageType("image/png") {
		t.Error("image/png should be recognized as image")
	}
	if isImageType("video/mp4") {
		t.Error("video/mp4 should NOT be image")
	}
	if isImageType("application/json") {
		t.Error("application/json should NOT be image")
	}
}

func TestIsVideoType(t *testing.T) {
	if !isVideoType("video/mp4") {
		t.Error("video/mp4 should be recognized as video")
	}
	if isVideoType("image/jpeg") {
		t.Error("image/jpeg should NOT be video")
	}
}

func TestIsAudioType(t *testing.T) {
	if !isAudioType("audio/mpeg") {
		t.Error("audio/mpeg should be recognized as audio")
	}
	if isAudioType("image/png") {
		t.Error("image/png should NOT be audio")
	}
}
