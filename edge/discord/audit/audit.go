// Package audit provides structured audit logging for Discord moderation
// decisions. It follows the same multi-emitter pattern as engine/audit but
// is simplified to stdout-only (slog JSON) since edge services have no DB
// access. All enforcement actions emit an audit event with required fields
// for DSA Art. 15 and GDPR Art. 22 compliance.
package audit

import (
	"context"
	"log/slog"
	"time"
)

// Emitter writes audit events to configured destinations.
// Only the slog stdout path is critical — failure there returns an error.
type Emitter interface {
	Emit(ctx context.Context, event AuditEvent) error
}

// AuditEvent is a structured audit record for Discord moderation actions.
// Mirrors engine/audit.AuditEvent with additional Discord-specific fields
// (guild_id, author_id, source_platform).
type AuditEvent struct {
	// WorkspaceID identifies the customer workspace.
	WorkspaceID string `json:"workspace_id"`

	// ContentID is the client-generated unique content identifier.
	ContentID string `json:"content_id"`

	// ContentHash is the BLAKE3 hash of the normalized content.
	ContentHash string `json:"content_hash"`

	// Decision is the moderation outcome: BLOCK, ALLOW, or QUEUED.
	Decision string `json:"decision"`

	// BlockReason is the human-readable reason for the block (DSA Art. 15).
	BlockReason string `json:"block_reason,omitempty"`

	// Category is the classification category (e.g., "violence_graphic").
	Category string `json:"category,omitempty"`

	// AnalystVersion identifies the AI moderation model version used.
	AnalystVersion string `json:"analyst_version,omitempty"`

	// GuildID is the Discord guild (server) ID where the message was posted.
	GuildID string `json:"guild_id"`

	// AuthorID is the Discord user ID of the message author.
	AuthorID string `json:"author_id"`

	// SourcePlatform is always "discord" for this adapter.
	SourcePlatform string `json:"source_platform"`

	// ElapsedMs is the total enforcement processing time in milliseconds.
	ElapsedMs int64 `json:"elapsed_ms"`

	// TimestampUTC is when the enforcement decision was rendered.
	TimestampUTC time.Time `json:"timestamp_utc"`
}

// SlogEmitter writes audit events as JSON lines to stdout via slog.
// This is the edge service adapter — no Neon DB or R2 storage.
// Matches the Emitter interface from engine/audit.
type SlogEmitter struct {
	logger *slog.Logger
}

// Compile-time interface check.
var _ Emitter = (*SlogEmitter)(nil)

// NewSlogEmitter creates an Emitter that writes audit events to stdout
// as structured JSON via slog.
func NewSlogEmitter(logger *slog.Logger) *SlogEmitter {
	return &SlogEmitter{logger: logger}
}

// Emit writes the audit event as a JSON log line to stdout.
// Required fields per spec §discord-moderation:
//
//	workspace_id, content_id, content_hash, decision, block_reason,
//	guild_id, author_id, source_platform, elapsed_ms, timestamp_utc.
func (e *SlogEmitter) Emit(ctx context.Context, event AuditEvent) error {
	e.logger.InfoContext(ctx, "audit_event",
		slog.String("event", "moderation_block"),
		slog.String("workspace_id", event.WorkspaceID),
		slog.String("content_id", event.ContentID),
		slog.String("content_hash", event.ContentHash),
		slog.String("decision", event.Decision),
		slog.String("block_reason", event.BlockReason),
		slog.String("category", event.Category),
		slog.String("analyst_version", event.AnalystVersion),
		slog.String("guild_id", event.GuildID),
		slog.String("author_id", event.AuthorID),
		slog.String("source_platform", event.SourcePlatform),
		slog.Int64("elapsed_ms", event.ElapsedMs),
		slog.Time("timestamp_utc", event.TimestampUTC),
	)
	return nil
}
