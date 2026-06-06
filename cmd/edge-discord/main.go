// Edge Discord Bot — main entrypoint for the Discord moderation bot.
// Wires all packages together: disgo gateway, ConnectRPC client, listener,
// slash commands, decision handler, rate limiter, and audit emitter.
//
// Graceful shutdown: signal.NotifyContext → drain in-flight RPCs → disgo.Close.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"

	"github.com/soyAurelio/AurelioMod/edge/discord/audit"
	"github.com/soyAurelio/AurelioMod/edge/discord/client"
	"github.com/soyAurelio/AurelioMod/edge/discord/commands"
	discordhandler "github.com/soyAurelio/AurelioMod/edge/discord/handler"
	"github.com/soyAurelio/AurelioMod/edge/discord/listener"
	"github.com/soyAurelio/AurelioMod/edge/discord/ratelimit"
)

func main() {
	// Structured JSON logging to stdout (slog → container log driver).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Required configuration from environment.
	discordToken := os.Getenv("DISCORD_TOKEN")
	if discordToken == "" {
		logger.Error("DISCORD_TOKEN is required")
		os.Exit(1)
	}

	engineURL := os.Getenv("ENGINE_URL")
	if engineURL == "" {
		engineURL = "http://engine:8080"
		logger.Warn("ENGINE_URL not set, using default", slog.String("engine_url", engineURL))
	}

	workspaceID := os.Getenv("REQUIRED_GUILD_ID")
	if workspaceID == "" {
		logger.Warn("REQUIRED_GUILD_ID not set — commands will be registered globally")
	}

	// Signal-aware context for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startTime := time.Now()
	logger.Info("edge-discord starting",
		slog.String("event", "startup"),
		slog.String("engine_url", engineURL),
	)

	// --- Core dependencies ---

	// AnalysisClient: ConnectRPC to Engine with circuit breaker.
	analysisClient := client.NewClient(engineURL, logger)

	// Rate limiter: 45 req/s token bucket with 2s queue deadline.
	limiter := ratelimit.NewLimiter(logger)

	// Audit emitter: stdout JSON lines (edge service has no DB).
	emitter := audit.NewSlogEmitter(logger)

	// --- Discord Bot ---

	// Gateway configuration: restrict to specific intents.
	gatewayOpts := []gateway.ConfigOpt{
		gateway.WithIntents(
			gateway.IntentGuilds,
			gateway.IntentGuildMessages,
			gateway.IntentMessageContent,
		),
	}

	disgoClient, err := disgo.New(discordToken,
		bot.WithLogger(logger),
		bot.WithGatewayConfigOpts(gatewayOpts...),
	)
	if err != nil {
		logger.Error("failed to create disgo client",
			slog.String("event", "disgo_create_failed"),
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	// --- Event Listener (MessageCreate filtering) ---
	l := listener.New(logger)

	// --- Decision Handler (BLOCK/ALLOW/QUEUED enforcement) ---
	decisionHandler := discordhandler.New(disgoClient.Rest, emitter, logger)

	// Wire event listeners.
	disgoClient.AddEventListeners(
		bot.NewListenerFunc(func(event bot.Event) {
			logger.InfoContext(ctx, "gateway event received",
				slog.String("event", "gateway_event"),
				slog.String("event_type", fmt.Sprintf("%T", event)),
			)
			switch e := event.(type) {
			case *events.MessageCreate:
				if l.OnMessageCreate(e) {
					handleMessage(ctx, e, analysisClient, limiter, decisionHandler, workspaceID, logger)
				}

			case *events.GuildJoin:
				l.OnGuildJoin(e)
				// Register slash commands in the new guild.
				syncCommands(ctx, disgoClient, e.Guild.ID)

			case *events.GuildReady:
				l.OnGuildReady(e)
				// Register slash commands on startup.
				syncCommands(ctx, disgoClient, e.Guild.ID)
			}
		}),
	)

	// --- Slash Command Router ---
	mux := handler.New()
	mux.Error(func(event *handler.InteractionEvent, err error) {
		logger.Error("interaction error",
			slog.String("event", "interaction_error"),
			slog.String("error", err.Error()),
		)
	})
	commands.Register(mux, analysisClient, limiter, startTime, workspaceID, logger)

	// Register the mux as an event listener.
	disgoClient.AddEventListeners(mux)

	// --- Start Gateway ---
	if err := disgoClient.OpenGateway(ctx); err != nil {
		logger.Error("failed to open gateway",
			slog.String("event", "gateway_open_failed"),
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	logger.Info("edge-discord running",
		slog.String("event", "running"),
		slog.String("bot_id", disgoClient.ID().String()),
	)

	// Wait for shutdown signal.
	<-ctx.Done()

	logger.Info("edge-discord shutting down",
		slog.String("event", "shutdown"),
	)

	// Graceful shutdown: drain in-flight RPCs with 10s deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	disgoClient.Close(shutdownCtx)

	logger.Info("edge-discord stopped",
		slog.String("event", "stopped"),
		slog.Duration("uptime", time.Since(startTime).Truncate(time.Second)),
	)
}

// handleMessage processes a filtered MessageCreate event:
// 1. Rate-limit check
// 2. Download CDN attachment binary (if present) or use message text
// 3. Build AnalyzeRequest with correct ContentType
// 4. Call Engine Analyze
// 5. Enforce decision
func handleMessage(ctx context.Context, event *events.MessageCreate, analysisClient client.AnalysisClient, limiter ratelimit.Limiter, decisionHandler *discordhandler.Handler, workspaceID string, logger *slog.Logger) {
	// Rate limit check.
	if err := limiter.Wait(ctx); err != nil {
		return // dropped
	}

	logger.InfoContext(ctx, "processing message",
		slog.String("event", "processing_message"),
		slog.String("message_id", event.Message.ID.String()),
		slog.String("content", event.Message.Content),
		slog.Int("num_attachments", len(event.Message.Attachments)),
		slog.Int("num_embeds", len(event.Message.Embeds)),
	)

	var rawBytes []byte
	contentType := aureliomodv1.ContentType_CONTENT_TYPE_EXTERNAL_URL

	// Check for attachment binary download first (regular + embed images)
	var urlsToTry []string
	for _, att := range event.Message.Attachments {
		urlsToTry = append(urlsToTry, att.URL)
	}
	for _, embed := range event.Message.Embeds {
		if embed.Image != nil {
			urlsToTry = append(urlsToTry, embed.Image.URL)
		}
	}
	for _, url := range urlsToTry {
		logger.InfoContext(ctx, "checking attachment for download",
			slog.String("event", "checking_attachment"),
			slog.String("url", url),
			slog.Bool("is_cdn", listener.IsDiscordCDN(url)),
		)

		if listener.IsDiscordCDN(url) {
			downloaded, ct, err := listener.DownloadAttachment(ctx, url, listener.MaxAttachmentBytes)
			if err != nil {
				logger.WarnContext(ctx, "attachment download failed, falling back to text",
					slog.String("event", "attachment_download_failed"),
					slog.String("error", err.Error()),
					slog.String("url", url),
				)
				continue
			}
			rawBytes = downloaded
			logger.InfoContext(ctx, "attachment downloaded",
				slog.String("event", "attachment_downloaded"),
				slog.Int("bytes", len(downloaded)),
				slog.String("url", url),
			)
			// Determine content type from the first attachment's MIME
			for _, att := range event.Message.Attachments {
				if att.URL == url && att.ContentType != nil {
					contentType = attContentType(*att.ContentType)
					break
				}
			}
			_ = ct
			break // only download first valid attachment
		}
	}

	// Fallback: use message text content
	if len(rawBytes) == 0 {
		rawBytes = []byte(event.Message.Content)
		// URLs are EXTERNAL_URL, already set
	}

	req := &aureliomodv1.AnalyzeRequest{
		WorkspaceId:    workspaceID,
		ContentId:      event.Message.ID.String(),
		RawBytes:       rawBytes,
		ContentType:    contentType,
		SourcePlatform: aureliomodv1.SourcePlatform_SOURCE_PLATFORM_DISCORD,
	}

	resp, err := analysisClient.Analyze(ctx, req)
	if err != nil {
		logger.ErrorContext(ctx, "analysis_failed",
			slog.String("event", "analysis_failed"),
			slog.String("error", err.Error()),
			slog.String("message_id", event.Message.ID.String()),
		)
		return
	}

	// Enforce the decision.
	_ = decisionHandler.EnforceDecision(ctx, &event.Message, resp.Decision, resp.BlockReason)
}

// attContentType maps Discord MIME types to AurelioMod ContentType.
func attContentType(ct string) aureliomodv1.ContentType {
	switch {
	case isImageType(ct):
		return aureliomodv1.ContentType_CONTENT_TYPE_IMAGE
	case isVideoType(ct):
		return aureliomodv1.ContentType_CONTENT_TYPE_VIDEO
	case isAudioType(ct):
		return aureliomodv1.ContentType_CONTENT_TYPE_AUDIO
	default:
		return aureliomodv1.ContentType_CONTENT_TYPE_EXTERNAL_URL
	}
}

func isImageType(ct string) bool {
	return ct == "image/jpeg" || ct == "image/png" || ct == "image/gif" || ct == "image/webp"
}

func isVideoType(ct string) bool {
	return ct == "video/mp4" || ct == "video/webm" || ct == "video/quicktime"
}

func isAudioType(ct string) bool {
	return ct == "audio/mpeg" || ct == "audio/ogg" || ct == "audio/wav"
}

// syncCommands registers slash commands in a guild.
// If REQUIRED_GUILD_ID is set, only that guild gets commands (faster dev cycle).
func syncCommands(ctx context.Context, client *bot.Client, guildID snowflake.ID) {
	// Determine target guilds.
	var guildIDs []snowflake.ID
	requiredGuild := os.Getenv("REQUIRED_GUILD_ID")
	if requiredGuild == "" {
		// Global registration (can take up to 1 hour to propagate).
		guildIDs = nil
	} else {
		// Guild-specific registration (instant, good for development).
		targetID, err := snowflake.Parse(requiredGuild)
		if err == nil {
			guildIDs = []snowflake.ID{targetID}
		}
	}

	if err := handler.SyncCommands(client, commands.CommandDefs(), guildIDs); err != nil {
		client.Logger.Error("sync_commands_failed",
			slog.String("event", "sync_commands_failed"),
			slog.String("error", err.Error()),
		)
		return
	}

	client.Logger.Info("slash commands registered",
		slog.String("event", "slash_commands_registered"),
		slog.String("guild_id", guildID.String()),
	)
}
