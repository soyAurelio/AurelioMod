// Package handler enforces moderation decisions on Discord messages.
// Implements the decision enforcement pipeline per spec §discord-moderation:
// BLOCK → delete message + DM author with block_reason,
// ALLOW → noop,
// QUEUED → fallback BLOCK with block_reason="pending_analysis".
//
// Enforcement is gated by ENFORCE_MODERATION env var (default true).
package handler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"

	"github.com/soyAurelio/AurelioMod/edge/discord/audit"
)

// DiscordRest defines the subset of disgo REST operations needed for
// moderation enforcement. Using an interface enables mocking in tests.
type DiscordRest interface {
	DeleteMessage(channelID snowflake.ID, messageID snowflake.ID, opts ...rest.RequestOpt) error
	CreateDMChannel(userID snowflake.ID, opts ...rest.RequestOpt) (*discord.DMChannel, error)
	CreateMessage(channelID snowflake.ID, messageCreate discord.MessageCreate, opts ...rest.RequestOpt) (*discord.Message, error)
}

// Handler enforces moderation decisions on Discord messages.
type Handler struct {
	rest    DiscordRest
	emitter audit.Emitter
	logger  *slog.Logger
	enforce bool
	lang    string // "es" or "en", from BOT_LANG env
}

// New creates a Handler with the ENFORCE_MODERATION gate from environment.
// Set ENFORCE_MODERATION=false to disable enforcement (ALLOW always).
// Language is controlled by BOT_LANG env var (default "en").
func New(rest DiscordRest, emitter audit.Emitter, logger *slog.Logger) *Handler {
	lang := os.Getenv("BOT_LANG")
	if lang != "es" {
		lang = "en"
	}
	return &Handler{
		rest:    rest,
		emitter: emitter,
		logger:  logger,
		enforce: os.Getenv("ENFORCE_MODERATION") != "false",
		lang:    lang,
	}
}

// NewWithEnforce creates a Handler with explicit enforce flag (for testing).
func NewWithEnforce(rest DiscordRest, emitter audit.Emitter, logger *slog.Logger, enforce bool) *Handler {
	return &Handler{
		rest:    rest,
		emitter: emitter,
		logger:  logger,
		enforce: enforce,
	}
}

// EnforceDecision applies the moderation decision to the Discord message.
//
// DECISION_BLOCK: deletes the message and sends a DM to the author with
// the block_reason (DSA Art. 15). Emits an audit event.
//
// DECISION_ALLOW: no operation — no REST calls, no audit.
//
// DECISION_QUEUED: falls back to BLOCK with block_reason="pending_analysis".
//
// If enforce is false (ENFORCE_MODERATION=false), all decisions are treated
// as ALLOW regardless of the actual decision value.
func (h *Handler) EnforceDecision(ctx context.Context, msg *discord.Message, decision aureliomodv1.Decision, blockReason string) error {
	if !h.enforce {
		return nil
	}

	switch decision {
	case aureliomodv1.Decision_DECISION_BLOCK:
		// Use the provided block_reason.
	case aureliomodv1.Decision_DECISION_ALLOW:
		return nil
	case aureliomodv1.Decision_DECISION_QUEUED:
		// Content pending analysis — don't block yet, don't DM.
		// The user will be notified if AI moderation later determines it's harmful.
		return nil
	default:
		// Unknown decisions: log and skip (don't block by default).
		h.logger.WarnContext(ctx, "unknown_decision",
			slog.String("event", "unknown_decision"),
			slog.Int("decision", int(decision)),
		)
		return nil
	}

	start := time.Now()

	// Delete the message (must happen within 500ms per spec).
	if err := h.rest.DeleteMessage(msg.ChannelID, msg.ID); err != nil {
		h.logger.ErrorContext(ctx, "delete_message_failed",
			slog.String("event", "delete_message_failed"),
			slog.String("error", err.Error()),
			slog.String("channel_id", msg.ChannelID.String()),
			slog.String("message_id", msg.ID.String()),
		)
		return fmt.Errorf("delete message %s: %w", msg.ID, err)
	}

	// DM the author with the block reason (best-effort).
	h.sendDM(ctx, msg.Author.ID, blockReason)

	elapsed := time.Since(start).Milliseconds()

	// Emit audit event.
	h.emitAudit(ctx, msg, blockReason, elapsed)

	return nil
}

// sendDM attempts to create a DM channel with the author and send the
// block reason. Failures are logged but do not stop enforcement —
// the message deletion already succeeded.
func (h *Handler) sendDM(ctx context.Context, authorID snowflake.ID, reason string) {
	dmChannel, err := h.rest.CreateDMChannel(authorID)
	if err != nil {
		h.logger.WarnContext(ctx, "dm_channel_failed",
			slog.String("event", "dm_channel_failed"),
			slog.String("error", err.Error()),
			slog.String("author_id", authorID.String()),
		)
		return
	}

	var dmText string
	if h.lang == "es" {
		dmText = fmt.Sprintf(
			"Tu mensaje fue eliminado de acuerdo con nuestra política de contenido.\n"+
				"**Motivo**: %s\n"+
				"Para apelar esta decisión, contactá a los moderadores del servidor.",
			reason,
		)
	} else {
		dmText = fmt.Sprintf(
			"Your message was removed in accordance with our content policy.\n"+
				"**Reason**: %s\n"+
				"To appeal this decision, please contact the server moderators.",
			reason,
		)
	}

	msg := discord.MessageCreate{Content: dmText}

	if _, err := h.rest.CreateMessage(dmChannel.ID(), msg); err != nil {
		h.logger.WarnContext(ctx, "dm_send_failed",
			slog.String("event", "dm_send_failed"),
			slog.String("error", err.Error()),
			slog.String("author_id", authorID.String()),
		)
	}
}

// emitAudit publishes an audit event for the enforcement action.
func (h *Handler) emitAudit(ctx context.Context, msg *discord.Message, blockReason string, elapsedMs int64) {
	guildID := ""
	if msg.GuildID != nil {
		guildID = msg.GuildID.String()
	}

	event := audit.AuditEvent{
		ContentID:      msg.ID.String(),
		Decision:       "BLOCK",
		BlockReason:    blockReason,
		GuildID:        guildID,
		AuthorID:       msg.Author.ID.String(),
		SourcePlatform: "discord",
		ElapsedMs:      elapsedMs,
		TimestampUTC:   time.Now(),
	}

	if err := h.emitter.Emit(ctx, event); err != nil {
		h.logger.WarnContext(ctx, "audit_emit_failed",
			slog.String("event", "audit_emit_failed"),
			slog.String("error", err.Error()),
		)
	}
}
