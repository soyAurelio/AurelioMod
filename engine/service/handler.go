// Package service provides the ConnectRPC handler for the Engine's
// ContentAnalysisService. It validates incoming requests and delegates
// the analysis pipeline execution.
package service

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/soyAurelio/AurelioMod/engine/hasher"
	"github.com/soyAurelio/AurelioMod/engine/pipeline"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
	"github.com/soyAurelio/AurelioMod/proto/aureliomod/v1/aureliomodv1connect"
)

// Compile-time interface compliance check.
var _ aureliomodv1connect.ContentAnalysisServiceHandler = (*Handler)(nil)

// Handler implements the generated ConnectRPC ContentAnalysisServiceHandler.
// It validates requests at the edge and delegates content analysis to the Pipeline.
type Handler struct {
	pipeline    pipeline.Pipeline
	enforceMIME bool
}

// NewHandler creates a Handler backed by the given pipeline.
// When enforceMIME is true, Content-Type vs magic byte validation
// runs before pipeline execution.
func NewHandler(p pipeline.Pipeline, enforceMIME bool) *Handler {
	return &Handler{pipeline: p, enforceMIME: enforceMIME}
}

// Analyze validates the incoming request and delegates to the analysis pipeline.
//
// Validation:
//   - workspace_id must be non-empty
//   - raw_bytes must be non-empty
//
// Errors are mapped to ConnectRPC codes as specified in the design:
//
//	Empty fields → InvalidArgument
//	Pipeline (normalization) errors → Internal
//	Context deadline exceeded → DeadlineExceeded
func (h *Handler) Analyze(ctx context.Context, req *connect.Request[v1.AnalyzeRequest]) (*connect.Response[v1.AnalyzeResponse], error) {
	msg := req.Msg

	// Validate required fields
	if msg.WorkspaceId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	if len(msg.RawBytes) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("raw_bytes must not be empty"))
	}

	// MIME validation gate — rejects contradictory Content-Type before
	// any subprocess is spawned (FFmpeg, WaveSpeed, etc.).
	if h.enforceMIME {
		mimeStr := contentTypeToMIME(msg.ContentType)
		if mimeStr != "" {
			if err := hasher.ValidateContentType(msg.RawBytes, mimeStr); err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}
		}
	}

	slog.DebugContext(ctx, "analyze request received",
		"workspace_id", msg.WorkspaceId,
		"content_id", msg.ContentId,
		"content_type", msg.ContentType.String(),
	)

	// Delegate to the pipeline
	resp, err := h.pipeline.Execute(ctx, msg)
	if err != nil {
		slog.ErrorContext(ctx, "pipeline execution failed",
			"error", err,
			"workspace_id", msg.WorkspaceId,
		)

		// Map known error kinds to appropriate gRPC codes
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, connect.NewError(connect.CodeDeadlineExceeded, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	slog.DebugContext(ctx, "analyze response ready",
		"workspace_id", msg.WorkspaceId,
		"cache_level", resp.CacheLevel.String(),
		"decision", resp.Decision.String(),
		"processing_ms", resp.ProcessingMs,
	)

	return connect.NewResponse(resp), nil
}

// contentTypeToMIME maps the protobuf ContentType enum to a MIME string
// for comparison against magic-byte detection.
// Returns empty string for types that don't map cleanly (FINGERPRINT, etc.).
func contentTypeToMIME(ct v1.ContentType) string {
	switch ct {
	case v1.ContentType_CONTENT_TYPE_IMAGE:
		return "image/jpeg"
	case v1.ContentType_CONTENT_TYPE_GIF:
		return "image/gif"
	case v1.ContentType_CONTENT_TYPE_VIDEO:
		return "video/mp4"
	case v1.ContentType_CONTENT_TYPE_AUDIO:
		return "audio/mpeg"
	default:
		return ""
	}
}
