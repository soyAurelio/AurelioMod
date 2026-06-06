// Package listener provides Discord gateway attachment handling.
// When a MESSAGE_CREATE event includes attachments with CDN URLs,
// the listener downloads the binary content and passes it to the
// Engine for moderation analysis.
package listener

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

const (
	// MaxAttachmentBytes is the maximum attachment size the listener
	// will download. Attachments exceeding this limit are skipped.
	// 10MB as specified in the stack document.
	MaxAttachmentBytes = 10 << 20 // 10 MiB
)

// DownloadAttachment downloads the binary content of a Discord CDN
// attachment URL. Returns the bytes, the Content-Type header, and
// any error. Downloads are limited to maxBytes (spec R4.4: 10MB max).
//
// Non-200 responses return an error (spec R4.5: download failure
// is logged, message is skipped — not crashed).
func DownloadAttachment(ctx context.Context, cdnURL string, maxBytes int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdnURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("discord attachment request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("discord attachment download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.WarnContext(ctx, "discord CDN returned non-200",
			"status", resp.StatusCode,
			"url", cdnURL,
		)
		return nil, "", fmt.Errorf("discord CDN returned HTTP %d", resp.StatusCode)
	}

	// Limit reader to maxBytes to prevent memory exhaustion
	limited := io.LimitReader(resp.Body, maxBytes+1) // +1 to detect overflow
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("discord attachment read: %w", err)
	}

	if int64(len(body)) > maxBytes {
		return body[:maxBytes], resp.Header.Get("Content-Type"),
			fmt.Errorf("attachment exceeds %d byte limit", maxBytes)
	}

	contentType := resp.Header.Get("Content-Type")
	return body, contentType, nil
}

// MapContentType maps a Discord attachment MIME type to a proto ContentType.
// Used to set the correct ContentType on the AnalyzeRequest before passing
// to the Engine.
func MapContentType(mime string) v1.ContentType {
	// Extract base MIME type (before semicolon or parameters)
	if idx := strings.IndexByte(mime, ';'); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}

	switch {
	case strings.HasPrefix(mime, "image/gif"):
		return v1.ContentType_CONTENT_TYPE_GIF
	case strings.HasPrefix(mime, "image/"):
		return v1.ContentType_CONTENT_TYPE_IMAGE
	case strings.HasPrefix(mime, "video/"):
		return v1.ContentType_CONTENT_TYPE_VIDEO
	case strings.HasPrefix(mime, "audio/"):
		return v1.ContentType_CONTENT_TYPE_AUDIO
	default:
		return v1.ContentType_CONTENT_TYPE_UNSPECIFIED
	}
}

// IsDiscordCDN returns true if the URL matches a Discord CDN domain
// pattern (cdn.discordapp.com or media.discordapp.net) — spec R4.1.
func IsDiscordCDN(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return strings.Contains(host, "cdn.discordapp.com") ||
		strings.Contains(host, "media.discordapp.net")
}
