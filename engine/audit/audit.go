// Package audit provides immutable NIS2/GDPR-compliant audit logging for
// moderation decisions. Every decision produces a 10-field AuditEvent that is
// emitted to three destinations: slog JSON stdout (real-time), Neon DB
// (append-only, 90d retention), and R2 cold storage (12mo retention).
//
// The audit trail enables:
//   - GDPR Art. 22: right to human review of automated decisions
//   - NIS2 Art. 21: full pipeline traceability for security audits
//   - AI Act Art. 52: analyst version documentation
package audit

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"lukechampine.com/blake3"
)

// AuditEvent is a 10-field immutable audit record as specified in
// aureliomod-stack.md §4 (Estrategia de Audit Logging). Every moderation
// decision in the pipeline emits one AuditEvent.
type AuditEvent struct {
	// AuditID is a unique event identifier (e.g., "evt_abc123").
	AuditID string `json:"audit_id"`

	// WorkspaceID is the workspace that owns the content.
	WorkspaceID string `json:"workspace_id"`

	// ContentHash is the BLAKE3 hash of the normalized RGB24 pixels.
	ContentHash string `json:"content_hash"`

	// Decision is the moderation outcome (e.g., "blocked", "allowed").
	Decision string `json:"decision"`

	// Confidence is the AI model confidence score (0.0 to 1.0).
	Confidence float64 `json:"confidence"`

	// Category is the classification category (e.g., "violence_graphic").
	Category string `json:"category"`

	// AnalystVersion identifies the model version that produced the decision
	// (e.g., "AI moderation-v3.2"). Required for AI Act Art. 52 compliance.
	AnalystVersion string `json:"analyst_version"`

	// NormalizationPipeline describes the content normalization steps applied
	// before hashing (e.g., "480p+strip_exif+jpeg_q85").
	NormalizationPipeline string `json:"normalization_pipeline"`

	// ProcessingMs is the total pipeline processing time in milliseconds.
	ProcessingMs int64 `json:"processing_ms"`

	// TimestampUTC is when the decision was rendered (ISO 8601 UTC).
	TimestampUTC time.Time `json:"timestamp_utc"`
}

// AuditEmitter is the interface for emitting audit events to all configured
// destinations. Implementations write to slog, Neon DB, and R2 in parallel
// with different failure semantics (see MultiEmitter).
type AuditEmitter interface {
	// Emit writes an audit event to all configured destinations.
	// Returns an error only if the critical path (slog) fails.
	// Neon DB and R2 failures are logged as warnings, not returned.
	Emit(ctx context.Context, event AuditEvent) error
}

// generateAuditID creates a unique audit event identifier using BLAKE3 of
// the content hash concatenated with the nanosecond-precision timestamp.
// The prefix "evt_" ensures readability in logs and dashboards.
func generateAuditID(contentHash string, ts time.Time) string {
	input := fmt.Sprintf("%s:%d", contentHash, ts.UnixNano())
	hash := blake3.Sum256([]byte(input))
	return "evt_" + hex.EncodeToString(hash[:12])
}
