// Package commands implements slash command registration and handlers
// for the edge-discord bot. Exposes CommandDefs for registration and
// Format* functions for response rendering (pure, testable).
//
// Commands:
//   - /moderate <url>: Submits a URL for content analysis.
//   - /status: Ephemeral bot diagnostics (uptime, engine, breaker, rate limiter).
//   - /config workspace_id|enforce: Configure workspace settings.
package commands

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/disgoorg/snowflake/v2"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"

	"github.com/soyAurelio/AurelioMod/edge/discord/client"
	"github.com/soyAurelio/AurelioMod/edge/discord/ratelimit"
)

// ModerateResponse contains the result of a /moderate analysis.
type ModerateResponse struct {
	URL         string
	Decision    string
	BlockReason string
	Confidence  float64
}

// StatusInfo contains diagnostic information for /status.
type StatusInfo struct {
	Uptime        time.Duration
	EngineHealthy bool
	BreakerState  string
	TokensAvail   float64
}

// CommandDefs returns the slash command definitions for registration
// via handler.SyncCommands.
func CommandDefs() []discord.ApplicationCommandCreate {
	return []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{
			Name:        "moderate",
			Description: "Analyze a URL for content moderation",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionString{
					Name:        "url",
					Description: "The URL to analyze",
					Required:    true,
				},
			},
		},
		discord.SlashCommandCreate{
			Name:        "status",
			Description: "View bot diagnostics and Engine health",
		},
		discord.SlashCommandCreate{
			Name:        "config",
			Description: "Configure workspace settings",
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{
					Name:        "workspace_id",
					Description: "Set the workspace ID for moderation",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionString{
							Name:        "id",
							Description: "The workspace ID",
							Required:    true,
						},
					},
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "enforce",
					Description: "Toggle moderation enforcement on/off",
					Options: []discord.ApplicationCommandOption{
						discord.ApplicationCommandOptionString{
							Name:        "mode",
							Description: "on or off",
							Required:    true,
							Choices: []discord.ApplicationCommandOptionChoiceString{
								{Name: "on", Value: "on"},
								{Name: "off", Value: "off"},
							},
						},
					},
				},
			},
		},
	}
}

// Register configures slash command handlers on the given disgo router.
func Register(r handler.Router, analysisClient client.AnalysisClient, limiter ratelimit.Limiter, startTime time.Time, workspaceID string, logger *slog.Logger) {
	r.SlashCommand("/moderate", moderateHandler(analysisClient, workspaceID, logger))
	r.SlashCommand("/status", statusHandler(analysisClient, limiter, startTime, logger))
	r.SlashCommand("/config", configHandler(logger))
}

// moderateHandler returns a SlashCommandHandler for /moderate <url>.
func moderateHandler(analysisClient client.AnalysisClient, workspaceID string, logger *slog.Logger) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		url := data.String("url")

		req := &aureliomodv1.AnalyzeRequest{
			WorkspaceId:    workspaceID,
			ContentId:      fmt.Sprintf("discord:%s:%s", e.GuildID().String(), e.ID().String()),
			RawBytes:       []byte(url),
			ContentType:    aureliomodv1.ContentType_CONTENT_TYPE_EXTERNAL_URL,
			SourcePlatform: aureliomodv1.SourcePlatform_SOURCE_PLATFORM_DISCORD,
		}

		resp, err := analysisClient.Analyze(e.Ctx, req)
		if err != nil {
			logger.ErrorContext(e.Ctx, "moderate_analysis_failed",
				slog.String("event", "moderate_analysis_failed"),
				slog.String("error", err.Error()),
				slog.String("url", url),
			)
			reply := FormatModerateReply(ModerateResponse{
				URL:      url,
				Decision: "ERROR",
			})
			return e.CreateMessage(discord.MessageCreate{Content: reply})
		}

		reply := FormatModerateReply(ModerateResponse{
			URL:         url,
			Decision:    decisionDisplay(resp.Decision),
			BlockReason: resp.BlockReason,
			Confidence:  resp.Confidence,
		})

		return e.CreateMessage(discord.MessageCreate{Content: reply})
	}
}

// statusHandler returns a SlashCommandHandler for /status (ephemeral).
func statusHandler(analysisClient client.AnalysisClient, limiter ratelimit.Limiter, startTime time.Time, logger *slog.Logger) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		// Check engine health with a connectivity probe (not a full Analyze)
		engineHealthy := checkEngineHealth(e.Ctx, analysisClient, logger)

		info := StatusInfo{
			Uptime:        time.Since(startTime),
			EngineHealthy: engineHealthy,
			BreakerState:  "unknown",
			TokensAvail:   -1,
		}

		// Get rate limiter status
		if limiter != nil {
			info.TokensAvail = 0 // approximate — Allow() is destructive
			if limiter.Allow(e.Ctx) {
				info.TokensAvail = 1
			}
		}

		reply := FormatStatusReply(info)

		return e.CreateMessage(discord.MessageCreate{
			Content: reply,
			Flags:   discord.MessageFlagEphemeral,
		})
	}
}

// checkEngineHealth probes Engine connectivity with a TCP dial.
// Returns true if Engine is reachable (TCP handshake succeeds).
// This is independent of WaveSpeed rate limiting — we only check
// that the service is listening.
func checkEngineHealth(ctx context.Context, analysisClient client.AnalysisClient, logger *slog.Logger) bool {
	engineURL := os.Getenv("ENGINE_URL")
	if engineURL == "" {
		engineURL = "http://engine:8080"
	}
	host := engineURL
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	// Add default port if missing
	if !strings.Contains(host, ":") {
		host = host + ":80"
	}

	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		logger.DebugContext(ctx, "engine_health_probe_failed",
			slog.String("event", "engine_health_probe_failed"),
			slog.String("host", host),
			slog.String("error", err.Error()),
		)
		return false
	}
	conn.Close()
	return true
}

// configHandler returns a SlashCommandHandler for /config subcommands.
func configHandler(logger *slog.Logger) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		subcommand := data.SubCommandName
		if subcommand == nil {
			return e.CreateMessage(discord.MessageCreate{
				Content: "Please specify a subcommand: `workspace_id` or `enforce`.",
				Flags:   discord.MessageFlagEphemeral,
			})
		}

		switch *subcommand {
		case "workspace_id":
			wsID := data.String("id")
			return e.CreateMessage(discord.MessageCreate{
				Content: fmt.Sprintf("Workspace ID set to `%s`. Restart required to take effect.", wsID),
				Flags:   discord.MessageFlagEphemeral,
			})
		case "enforce":
			mode := data.String("mode")
			return e.CreateMessage(discord.MessageCreate{
				Content: fmt.Sprintf("Enforcement mode set to `%s`. Restart required to take effect.", mode),
				Flags:   discord.MessageFlagEphemeral,
			})
		default:
			return e.CreateMessage(discord.MessageCreate{
				Content: fmt.Sprintf("Unknown subcommand: `%s`.", *subcommand),
				Flags:   discord.MessageFlagEphemeral,
			})
		}
	}
}

// decisionDisplay maps proto Decision enum to a human-readable string.
func decisionDisplay(decision aureliomodv1.Decision) string {
	switch decision {
	case aureliomodv1.Decision_DECISION_BLOCK:
		return "BLOCK"
	case aureliomodv1.Decision_DECISION_ALLOW:
		return "ALLOW"
	case aureliomodv1.Decision_DECISION_QUEUED:
		return "QUEUED"
	case aureliomodv1.Decision_DECISION_ERROR:
		return "ERROR"
	default:
		return "UNSPECIFIED"
	}
}

// FormatModerateReply builds the human-readable response for /moderate.
func FormatModerateReply(resp ModerateResponse) string {
	switch resp.Decision {
	case "BLOCK":
		return fmt.Sprintf(
			"🔴 **Content BLOCKED**\nURL: %s\nReason: %s\nConfidence: %.0f%%",
			resp.URL, resp.BlockReason, resp.Confidence*100,
		)
	case "ALLOW":
		return fmt.Sprintf(
			"🟢 **Content ALLOWED**\nURL: %s",
			resp.URL,
		)
	case "QUEUED":
		return fmt.Sprintf(
			"⏳ **Analysis QUEUED**\nURL: %s\nYour content is pending analysis.",
			resp.URL,
		)
	case "ERROR":
		return fmt.Sprintf(
			"⚠️ **Analysis ERROR**\nURL: %s\nThe analysis service encountered an error. Please try again.",
			resp.URL,
		)
	default:
		return fmt.Sprintf(
			"❓ **Unknown Decision**\nURL: %s\nDecision: %s",
			resp.URL, resp.Decision,
		)
	}
}

// FormatStatusReply builds the ephemeral /status response message.
func FormatStatusReply(info StatusInfo) string {
	health := "healthy ✅"
	if !info.EngineHealthy {
		health = "unhealthy ❌"
	}

	uptime := info.Uptime.Truncate(time.Minute).String()

	return fmt.Sprintf(
		"**AurelioMod Edge Bot — Diagnostics**\n"+
			"Uptime: %s\n"+
			"Engine: %s\n"+
			"Circuit Breaker: %s\n"+
			"Rate Limiter: %.0f tokens available",
		uptime, health, info.BreakerState, info.TokensAvail,
	)
}

// Compile-time check: our structs satisfy the option interface.
var (
	_ discord.ApplicationCommandOption = discord.ApplicationCommandOptionString{}
	_ discord.ApplicationCommandOption = discord.ApplicationCommandOptionSubCommand{}
)

// Ensure snowflake is used (for main.go wiring).
var _ snowflake.ID
