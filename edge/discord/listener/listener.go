// Package listener provides Discord event handlers for the edge service.
// It intercepts MessageCreate events to filter content for moderation
// (attachments and URLs), downloads Discord CDN binary content, and
// maps MIME types to proto ContentTypes. GuildCreate events trigger
// slash command registration.
//
// The filtering logic is extracted into pure functions for testability.
package listener

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

const (
	// MaxAttachmentBytes is the maximum attachment size the listener
	// will download. Attachments exceeding this limit are skipped.
	MaxAttachmentBytes = 10 << 20 // 10 MiB
)

// maxAttachmentSize is the maximum attachment size in bytes (10 MiB)
// for the shouldAnalyze filter.
const maxAttachmentSize = 10 * 1024 * 1024

// MessageData is the subset of discord.Message fields needed for
// content filtering. Decoupled from disgo for pure-function testing.
type MessageData struct {
	Attachments []AttachmentData
	Content     string
	GuildID     string
	AuthorID    string
}

// AttachmentData mirrors discord.Attachment fields needed for filtering.
type AttachmentData struct {
	Filename string
	URL      string
	Size     int
}

// Listener handles Discord gateway events for the edge service.
type Listener struct {
	logger *slog.Logger
}

// New creates a Listener with the given structured logger.
func New(logger *slog.Logger) *Listener {
	return &Listener{logger: logger}
}

// OnMessageCreate handles incoming Discord MessageCreate events.
// It filters messages for content moderation: attachments and URLs
// trigger analysis; plain text and oversize attachments are skipped.
// Returns true if the message should be forwarded for analysis.
func (l *Listener) OnMessageCreate(event *events.MessageCreate) bool {
	// Never process bot messages (including our own).
	if event.Message.Author.Bot {
		return false
	}

	data := MessageData{
		Content:  event.Message.Content,
		GuildID:  guildIDString(event.Message.GuildID),
		AuthorID: event.Message.Author.ID.String(),
	}
	for _, att := range event.Message.Attachments {
		data.Attachments = append(data.Attachments, AttachmentData{
			Filename: att.Filename,
			URL:      att.URL,
			Size:     att.Size,
		})
	}
	// Forwarded messages with images come as embeds, not attachments.
	for _, embed := range event.Message.Embeds {
		if embed.Image != nil {
			data.Attachments = append(data.Attachments, AttachmentData{
				Filename: "embed_image",
				URL:      embed.Image.URL,
				Size:     embed.Image.Width * embed.Image.Height,
			})
		}
	}

	return l.shouldAnalyze(data)
}

// OnGuildJoin handles GuildJoin events (bot joins a Discord guild).
func (l *Listener) OnGuildJoin(event *events.GuildJoin) {
	l.logger.InfoContext(context.Background(), "guild_join",
		slog.String("event", "guild_join"),
		slog.String("guild_id", event.Guild.ID.String()),
		slog.String("guild_name", event.Guild.Name),
	)
}

// OnGuildReady handles GuildReady events (guild loaded, including startup).
func (l *Listener) OnGuildReady(event *events.GuildReady) {
	l.logger.InfoContext(context.Background(), "guild_ready",
		slog.String("event", "guild_ready"),
		slog.String("guild_id", event.Guild.ID.String()),
		slog.String("guild_name", event.Guild.Name),
	)
}

// shouldAnalyze performs the core filtering logic for MessageData.
func (l *Listener) shouldAnalyze(msg MessageData) bool {
	return shouldAnalyzeWithLog(l.logger, msg)
}

// shouldAnalyze is the pure-function version for testing without a Listener.
func shouldAnalyze(msg MessageData) bool {
	return shouldAnalyzeWithLog(slog.Default(), msg)
}

// shouldAnalyzeWithLog contains the core filtering logic.
func shouldAnalyzeWithLog(logger *slog.Logger, msg MessageData) bool {
	hasURLs := strings.Contains(msg.Content, "http://") ||
		strings.Contains(msg.Content, "https://")

	validAttachment := false
	for _, att := range msg.Attachments {
		if att.Size > maxAttachmentSize {
			logger.WarnContext(context.Background(), "attachment too large, skipping",
				slog.String("event", "attachment_too_large"),
				slog.Int("size_bytes", att.Size),
				slog.String("filename", att.Filename),
				slog.Int("max_bytes", maxAttachmentSize),
			)
		} else {
			validAttachment = true
		}
	}

	return validAttachment || hasURLs
}

// guildIDString converts a snowflake guild ID pointer to string.
func guildIDString(id *snowflake.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// DownloadAttachment downloads the binary content of a Discord CDN
// attachment URL. Returns the bytes, the Content-Type header, and
// any error. Downloads are limited to MaxAttachmentBytes (10MB).
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

	limited := io.LimitReader(resp.Body, maxBytes+1)
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
func MapContentType(mime string) v1.ContentType {
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

// IsDiscordCDN returns true if the URL matches a Discord CDN domain pattern.
func IsDiscordCDN(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return strings.Contains(host, "cdn.discordapp.com") ||
		strings.Contains(host, "media.discordapp.net")
}
