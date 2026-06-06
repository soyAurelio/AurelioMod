package listener

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// --- shouldAnalyze tests (from edge-discord PR1) ---

func TestShouldAnalyze_PlainTextSkipped(t *testing.T) {
	msg := MessageData{Content: "hello world", GuildID: "123"}
	if shouldAnalyze(msg) {
		t.Error("plain text without URL or attachment should NOT trigger analysis")
	}
}

func TestShouldAnalyze_EmptyMessageSkipped(t *testing.T) {
	msg := MessageData{Content: "", GuildID: "123"}
	if shouldAnalyze(msg) {
		t.Error("empty message should NOT trigger analysis")
	}
}

func TestShouldAnalyze_ImageAttachment(t *testing.T) {
	msg := MessageData{
		Content: "check this",
		GuildID: "123",
		Attachments: []AttachmentData{
			{Filename: "photo.png", URL: "https://cdn.discordapp.com/attachments/1/2/photo.png", Size: 1024},
		},
	}
	if !shouldAnalyze(msg) {
		t.Error("message with image attachment should trigger analysis")
	}
}

func TestShouldAnalyze_URLTriggersAnalysis(t *testing.T) {
	msg := MessageData{
		Content: "check this https://example.com/image.jpg",
		GuildID: "123",
	}
	if !shouldAnalyze(msg) {
		t.Error("message with URL should trigger analysis")
	}
}

func TestShouldAnalyze_LargeAttachmentSkipped(t *testing.T) {
	msg := MessageData{
		Content: "check this",
		GuildID: "123",
		Attachments: []AttachmentData{
			{Filename: "bigfile.mp4", URL: "https://cdn.discordapp.com/attachments/1/2/bigfile.mp4", Size: 11 * 1024 * 1024},
		},
	}
	if shouldAnalyze(msg) {
		t.Error("message with over-10MB attachment should NOT trigger analysis")
	}
}

func TestShouldAnalyze_MixedAttachments(t *testing.T) {
	msg := MessageData{
		Content: "check this",
		GuildID: "123",
		Attachments: []AttachmentData{
			{Filename: "bigfile.mp4", URL: "https://cdn.discordapp.com/attachments/1/2/bigfile.mp4", Size: 11 * 1024 * 1024},
			{Filename: "small.png", URL: "https://cdn.discordapp.com/attachments/1/2/small.png", Size: 1024},
		},
	}
	if !shouldAnalyze(msg) {
		t.Error("message with mixed attachments (one valid) should trigger analysis")
	}
}

func TestShouldAnalyze_NoAttachmentsNoURL(t *testing.T) {
	msg := MessageData{
		Content: "just some text",
		GuildID: "123",
	}
	if shouldAnalyze(msg) {
		t.Error("message with no attachments and no URL should NOT trigger analysis")
	}
}

func TestShouldAnalyze_MultipleURLs(t *testing.T) {
	msg := MessageData{
		Content: "https://link1.com and https://link2.com",
		GuildID: "333",
	}
	if !shouldAnalyze(msg) {
		t.Error("message with multiple URLs should be analyzed")
	}
}

// --- DownloadAttachment / MapContentType / IsDiscordCDN tests (from engine-wavefix PR3) ---

func TestDownloadAttachment_Success(t *testing.T) {
	expectedBody := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG magic
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(expectedBody)
	}))
	defer srv.Close()

	ctx := t.Context()
	body, ct, err := DownloadAttachment(ctx, srv.URL, MaxAttachmentBytes)
	if err != nil {
		t.Fatalf("DownloadAttachment() error = %v", err)
	}
	if !bytes.Equal(body, expectedBody) {
		t.Errorf("body mismatch: got %x, want %x", body, expectedBody)
	}
	if ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
}

func TestDownloadAttachment_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ctx := t.Context()
	_, _, err := DownloadAttachment(ctx, srv.URL, MaxAttachmentBytes)
	if err == nil {
		t.Error("DownloadAttachment() expected error for 404, got nil")
	}
}

func TestDownloadAttachment_MaxSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(bytes.Repeat([]byte{0xFF}, 200*1024)) // 200KB, maxBytes is 100KB
	}))
	defer srv.Close()

	ctx := t.Context()
	const maxBytes int64 = 100 << 10 // 100KB
	_, _, err := DownloadAttachment(ctx, srv.URL, maxBytes)
	if err == nil {
		t.Error("DownloadAttachment() expected overflow error, got nil")
	}
}

func TestMapContentType(t *testing.T) {
	tests := []struct {
		mime string
		want v1.ContentType
	}{
		{"image/png", v1.ContentType_CONTENT_TYPE_IMAGE},
		{"image/jpeg", v1.ContentType_CONTENT_TYPE_IMAGE},
		{"image/gif", v1.ContentType_CONTENT_TYPE_GIF},
		{"image/webp", v1.ContentType_CONTENT_TYPE_IMAGE},
		{"video/mp4", v1.ContentType_CONTENT_TYPE_VIDEO},
		{"video/webm", v1.ContentType_CONTENT_TYPE_VIDEO},
		{"audio/mpeg", v1.ContentType_CONTENT_TYPE_AUDIO},
		{"audio/ogg", v1.ContentType_CONTENT_TYPE_AUDIO},
		{"text/plain", v1.ContentType_CONTENT_TYPE_UNSPECIFIED},
		{"image/png; charset=utf-8", v1.ContentType_CONTENT_TYPE_IMAGE}, // with parameters
		{"", v1.ContentType_CONTENT_TYPE_UNSPECIFIED},
	}
	for _, tt := range tests {
		got := MapContentType(tt.mime)
		if got != tt.want {
			t.Errorf("MapContentType(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestIsDiscordCDN(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://cdn.discordapp.com/attachments/1/2/file.png", true},
		{"https://media.discordapp.net/attachments/1/2/file.png", true},
		{"https://example.com/file.png", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, tt := range tests {
		got := IsDiscordCDN(tt.url)
		if got != tt.want {
			t.Errorf("IsDiscordCDN(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestDownloadAttachment_LargeResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
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
}
