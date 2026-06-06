// Package listener provides Discord event handlers for the edge service.
// It intercepts MessageCreate events to filter content for moderation
// (attachments and URLs) and GuildCreate events to register slash commands.
//
// The filtering logic is extracted into pure functions for testability.
package listener

import (
	"context"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

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

// maxAttachmentSize is the maximum attachment size in bytes (10 MiB).
const maxAttachmentSize = 10 * 1024 * 1024

// Listener handles Discord gateway events for the edge service.
// In PR 1 it provides filtering logic; PR 2 adds the full handler
// pipeline (client, handler, command registration).
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
	return l.shouldAnalyze(data)
}

// OnGuildJoin handles GuildJoin events (bot joins a Discord guild).
// Registers slash commands for the guild. In PR 1 this is a stub;
// PR 2 adds command registration via disgo.
func (l *Listener) OnGuildJoin(event *events.GuildJoin) {
	l.logger.InfoContext(context.Background(), "guild_join",
		slog.String("event", "guild_join"),
		slog.String("guild_id", event.Guild.ID.String()),
		slog.String("guild_name", event.Guild.Name),
	)
}

// OnGuildReady handles GuildReady events (guild becomes loaded, including startup).
// Also registers slash commands. In PR 1 this is a stub.
func (l *Listener) OnGuildReady(event *events.GuildReady) {
	l.logger.InfoContext(context.Background(), "guild_ready",
		slog.String("event", "guild_ready"),
		slog.String("guild_id", event.Guild.ID.String()),
		slog.String("guild_name", event.Guild.Name),
	)
}

// shouldAnalyze performs the core filtering logic for MessageData.
// Returns true if the message contains attachments or URLs that should
// be analyzed, false otherwise.
func (l *Listener) shouldAnalyze(msg MessageData) bool {
	return shouldAnalyzeWithLog(l.logger, msg)
}

// shouldAnalyze is the pure-function version for testing without a Listener.
func shouldAnalyze(msg MessageData) bool {
	return shouldAnalyzeWithLog(slog.Default(), msg)
}

// shouldAnalyzeWithLog contains the core filtering logic with configurable logger.
// Returns true when the message has valid attachments (≤10MB) or URLs.
// Oversize attachments are logged as warnings and do NOT trigger analysis
// unless there is also a valid attachment or URL.
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
