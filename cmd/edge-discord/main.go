// Command edge-discord is the Discord gateway edge service for AurelioMod.
// It connects to the Discord Gateway, listens for MESSAGE_CREATE events,
// downloads CDN attachments, and submits them to the Engine for moderation.
//
// Feature gates (all default to safe values):
//
//	ATTACHMENT_ANALYSIS_ENABLED=true   Download and analyze CDN attachments
//	ENGINE_URL=http://localhost:9090    Engine ConnectRPC endpoint
//
// Structured logging via slog with JSON output to stdout.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
	aureliomodv1connect "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1/aureliomodv1connect"

	"github.com/soyAurelio/AurelioMod/edge/discord/listener"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	engineURL := cmpOr(os.Getenv("ENGINE_URL"), "http://localhost:9090")
	attachmentEnabled := envBool("ATTACHMENT_ANALYSIS_ENABLED", true)

	logger.Info("edge-discord starting",
		"engine_url", engineURL,
		"attachment_analysis_enabled", attachmentEnabled,
	)

	// Connect to Engine via ConnectRPC
	engineClient := aureliomodv1connect.NewContentAnalysisServiceClient(
		http.DefaultClient,
		engineURL,
	)
	_ = engineClient // used in handleAttachment when Discord gateway is wired
	_ = handleAttachment

	// Create a context that cancels on SIGTERM/SIGINT
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	logger.Info("edge-discord ready",
		"attachment_analysis_enabled", attachmentEnabled,
	)

	// Placeholder: Discord gateway connection would be established here.
	// For now, the service is ready to receive MESSAGE_CREATE events from
	// a Discord gateway library (e.g., discordgo).
	//
	// Event handler pattern:
	//   on MESSAGE_CREATE(msg):
	//     for each msg.Attachments:
	//       if listener.IsDiscordCDN(att.URL):
	//         handleAttachment(ctx, engineClient, msg, att)

	<-ctx.Done()
	logger.Info("edge-discord shutting down")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = shutdownCtx
}

// handleAttachment downloads a Discord CDN attachment and submits it
// to the Engine for analysis. The content type is mapped from the
// attachment's MIME type and raw bytes are passed directly.
//
// Spec R4.3: Downloaded bytes SHALL be passed in RawBytes field.
// Spec R4.5: Download failure SHALL be logged; message skipped.
func handleAttachment(
	ctx context.Context,
	client aureliomodv1connect.ContentAnalysisServiceClient,
	workspaceID, contentID, cdnURL, contentType string,
) error {
	bytes, respType, err := listener.DownloadAttachment(ctx, cdnURL, listener.MaxAttachmentBytes)
	if err != nil {
		slog.ErrorContext(ctx, "attachment download failed, skipping",
			"url", cdnURL,
			"error", err,
			"content_id", contentID,
		)
		return fmt.Errorf("download attachment: %w", err)
	}

	ct := listener.MapContentType(respType)
	if ct == aureliomodv1.ContentType_CONTENT_TYPE_UNSPECIFIED && contentType != "" {
		// Fall back to the Discord-provided content type if the
		// downloaded MIME type is not recognized
		ct = listener.MapContentType(contentType)
	}

	req := connect.NewRequest(&aureliomodv1.AnalyzeRequest{
		WorkspaceId:    workspaceID,
		ContentId:      contentID,
		RawBytes:       bytes,
		ContentType:    ct,
		SourcePlatform: aureliomodv1.SourcePlatform_SOURCE_PLATFORM_DISCORD,
	})

	resp, err := client.Analyze(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "engine analysis failed",
			"url", cdnURL,
			"error", err,
			"content_id", contentID,
		)
		return fmt.Errorf("engine analyze: %w", err)
	}

	slog.InfoContext(ctx, "attachment analyzed",
		"decision", resp.Msg.Decision.String(),
		"confidence", resp.Msg.Confidence,
		"category", resp.Msg.Category,
		"content_id", contentID,
	)

	return nil
}

// cmpOr returns the first non-empty string argument.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// envBool reads a boolean environment variable with a default.
func envBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	val = strings.ToLower(strings.TrimSpace(val))
	return val == "true" || val == "1" || val == "yes" || val == "on"
}
