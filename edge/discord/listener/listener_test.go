package listener

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// TestShouldAnalyze_PlainTextSkipped verifies that messages with no
// attachments and no URLs are skipped.
func TestShouldAnalyze_PlainTextSkipped(t *testing.T) {
	msg := MessageData{
		Content: "hello world, just chatting",
		GuildID: "123",
	}

	if shouldAnalyze(msg) {
		t.Error("plain text message should NOT be analyzed")
	}
}

// TestShouldAnalyze_EmptyMessageSkipped verifies empty messages are skipped.
func TestShouldAnalyze_EmptyMessageSkipped(t *testing.T) {
	msg := MessageData{
		Content: "",
		GuildID: "123",
	}

	if shouldAnalyze(msg) {
		t.Error("empty message should NOT be analyzed")
	}
}

// TestShouldAnalyze_ImageAttachment triggers analysis for small images.
func TestShouldAnalyze_ImageAttachment(t *testing.T) {
	msg := MessageData{
		Content: "check this image",
		GuildID: "456",
		Attachments: []AttachmentData{
			{
				Filename: "photo.png",
				URL:      "https://cdn.discord.com/attachments/123/photo.png",
				Size:     512 * 1024, // 512KB
			},
		},
	}

	if !shouldAnalyze(msg) {
		t.Error("message with small image attachment should be analyzed")
	}
}

// TestShouldAnalyze_URLTriggersAnalysis tests that messages with URLs
// but no attachments trigger analysis.
func TestShouldAnalyze_URLTriggersAnalysis(t *testing.T) {
	msg := MessageData{
		Content: "check out https://example.com/video.mp4",
		GuildID: "789",
	}

	if !shouldAnalyze(msg) {
		t.Error("message with URL should be analyzed")
	}
}

// TestShouldAnalyze_LargeAttachmentSkipped verifies that attachments >10MB
// are skipped and a warning is logged.
func TestShouldAnalyze_LargeAttachmentSkipped(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	msg := MessageData{
		Content: "big file incoming",
		GuildID: "999",
		Attachments: []AttachmentData{
			{
				Filename: "large_video.mp4",
				URL:      "https://cdn.discord.com/attachments/999/large_video.mp4",
				Size:     15 * 1024 * 1024, // 15MB
			},
		},
	}

	result := shouldAnalyzeWithLog(logger, msg)
	if result {
		t.Error("message with >10MB attachment should be skipped")
	}

	// Verify warning was logged
	output := buf.String()
	if !strings.Contains(output, "attachment too large") {
		t.Errorf("Expected 'attachment too large' warning, got: %s", output)
	}
}

// TestShouldAnalyze_MixedAttachments tests that a message with a small
// and large attachment is still analyzed (small ones pass the filter).
func TestShouldAnalyze_MixedAttachments(t *testing.T) {
	msg := MessageData{
		Content: "mixed files",
		GuildID: "111",
		Attachments: []AttachmentData{
			{
				Filename: "small.jpg",
				Size:     100 * 1024, // 100KB
			},
		},
	}

	if !shouldAnalyze(msg) {
		t.Error("message with small attachment should be analyzed")
	}
}

// TestShouldAnalyze_NoAttachmentsNoURL tests edge case of message with
// only whitespace content and no attachments.
func TestShouldAnalyze_NoAttachmentsNoURL(t *testing.T) {
	msg := MessageData{
		Content: "   ",
		GuildID: "222",
	}

	if shouldAnalyze(msg) {
		t.Error("whitespace-only message should NOT be analyzed")
	}
}

// TestShouldAnalyze_MultipleURLs tests message with multiple URLs.
func TestShouldAnalyze_MultipleURLs(t *testing.T) {
	msg := MessageData{
		Content: "https://link1.com and https://link2.com",
		GuildID: "333",
	}

	if !shouldAnalyze(msg) {
		t.Error("message with multiple URLs should be analyzed")
	}
}
// TestDownloadAttachment_Success verifies that DownloadAttachment correctly
// downloads bytes from a CDN URL and returns them with the Content-Type header.
func TestDownloadAttachment_Success(t *testing.T) {
	expectedBody := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG magic
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(expectedBody)
	}))
	defer srv.Close()

	ctx := t.Context()
	got, contentType, err := DownloadAttachment(ctx, srv.URL, 10<<20) // 10MB max
	if err != nil {
		t.Fatalf("DownloadAttachment() unexpected error: %v", err)
	}
	if contentType != "image/png" {
		t.Errorf("contentType = %q, want %q", contentType, "image/png")
	}
	if len(got) != len(expectedBody) {
		t.Errorf("len(bytes) = %d, want %d", len(got), len(expectedBody))
	}
}

// TestDownloadAttachment_HTTPError verifies that non-200 responses (404, 403)
// produce an error and are not treated as valid attachments (spec R4.5).
func TestDownloadAttachment_HTTPError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"404 not found", http.StatusNotFound, "not found"},
		{"403 forbidden", http.StatusForbidden, "forbidden"},
		{"500 internal error", http.StatusInternalServerError, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			ctx := t.Context()
			_, _, err := DownloadAttachment(ctx, srv.URL, 10<<20)
			if err == nil {
				t.Errorf("DownloadAttachment() expected error for status %d, got nil", tt.status)
			}
		})
	}
}

// TestDownloadAttachment_MaxSize verifies that downloads exceeding maxBytes
// are truncated and an error is returned (spec: 10MB max).
func TestDownloadAttachment_MaxSize(t *testing.T) {
	// Serve 1MB of data, set max to 512KB
	bigBody := make([]byte, 1<<20) // 1MB
	for i := range bigBody {
		bigBody[i] = byte(i % 256)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(bigBody)
	}))
	defer srv.Close()

	ctx := t.Context()
	const maxBytes int64 = 512 << 10 // 512KB

	got, _, err := DownloadAttachment(ctx, srv.URL, maxBytes)
	if err == nil {
		t.Error("DownloadAttachment() expected error for oversized attachment, got nil")
	}
	// Even on error, we should have partial data up to the limit
	if int64(len(got)) > maxBytes {
		t.Errorf("len(bytes) = %d, should not exceed maxBytes=%d", len(got), maxBytes)
	}
}

// TestMapContentType verifies that Discord MIME types map to proto ContentType
// values correctly for attachment analysis.
func TestMapContentType(t *testing.T) {
	tests := []struct {
		mime string
		want v1.ContentType
	}{
		{"image/png", v1.ContentType_CONTENT_TYPE_IMAGE},
		{"image/jpeg", v1.ContentType_CONTENT_TYPE_IMAGE},
		{"image/webp", v1.ContentType_CONTENT_TYPE_IMAGE},
		{"image/gif", v1.ContentType_CONTENT_TYPE_GIF},
		{"video/mp4", v1.ContentType_CONTENT_TYPE_VIDEO},
		{"video/webm", v1.ContentType_CONTENT_TYPE_VIDEO},
		{"audio/mp3", v1.ContentType_CONTENT_TYPE_AUDIO},
		{"audio/ogg", v1.ContentType_CONTENT_TYPE_AUDIO},
		{"audio/mpeg", v1.ContentType_CONTENT_TYPE_AUDIO},
		{"application/octet-stream", v1.ContentType_CONTENT_TYPE_UNSPECIFIED},
		{"text/plain", v1.ContentType_CONTENT_TYPE_UNSPECIFIED},
		{"", v1.ContentType_CONTENT_TYPE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			got := MapContentType(tt.mime)
			if got != tt.want {
				t.Errorf("MapContentType(%q) = %v, want %v", tt.mime, got, tt.want)
			}
		})
	}
}

// TestIsDiscordCDN verifies CDN URL pattern matching for both
// cdn.discordapp.com and media.discordapp.net domains (spec R4.1).
func TestIsDiscordCDN(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://cdn.discordapp.com/attachments/123/456/file.png", true},
		{"https://media.discordapp.net/attachments/123/456/file.png", true},
		{"http://cdn.discordapp.com/attachments/123/456/file.jpg", true},
		{"https://cdn.discordapp.com/attachments/123/456/", true},
		{"https://discord.com/channels/123/456", false},
		{"https://example.com/file.png", false},
		{"", false},
		{"not-a-url", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := IsDiscordCDN(tt.url)
			if got != tt.want {
				t.Errorf("IsDiscordCDN(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

// TestDownloadAttachment_LargeResponseBody verifies that the reader is
// limited to maxBytes even when Content-Length is not set (streaming response).
func TestDownloadAttachment_LargeResponseBody(t *testing.T) {
	// Infinite reader pattern
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// Write 1MB of zeros regardless of Content-Length
		chunk := make([]byte, 4096)
		for i := 0; i < 256; i++ { // 256 * 4096 = 1MB
			w.Write(chunk)
		}
	}))
	defer srv.Close()

	ctx := t.Context()
	const maxBytes int64 = 100 << 10 // 100KB limit

	got, _, err := DownloadAttachment(ctx, srv.URL, maxBytes)
	if err == nil {
		t.Error("DownloadAttachment() expected error for oversized stream, got nil")
	}
	if int64(len(got)) > maxBytes {
		t.Errorf("len(bytes) = %d, should not exceed maxBytes=%d", len(got), maxBytes)
	}
