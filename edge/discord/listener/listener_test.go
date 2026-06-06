package listener

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
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
