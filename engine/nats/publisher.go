// Package nats provides NATS JetStream publishing of moderation decisions
// for real-time dashboard updates via Centrifugo WebSocket relay.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// DecisionEvent is the payload published to NATS when a moderation decision
// is produced by the pipeline. Centrifugo subscribes to NATS and relays
// these events to the dashboard via WebSocket.
type DecisionEvent struct {
	// DecisionID is a unique identifier for this decision event.
	DecisionID string `json:"decision_id"`

	// WorkspaceID is the workspace that owns the content.
	WorkspaceID string `json:"workspace_id"`

	// ContentHash is the BLAKE3 hash of the normalized content.
	ContentHash string `json:"content_hash"`

	// Decision is the moderation outcome (e.g., "blocked", "allowed").
	Decision string `json:"decision"`

	// Category is the classification category (e.g., "violence_graphic").
	Category string `json:"category"`

	// Confidence is the AI model confidence score (0.0 to 1.0).
	Confidence float64 `json:"confidence"`

	// Timestamp is when the decision was produced (ISO 8601 UTC).
	Timestamp time.Time `json:"timestamp"`
}

// DecisionPublisher publishes moderation decisions to NATS for real-time
// dashboard updates. Implementations are fire-and-forget — failures are
// logged but do not affect the request pipeline.
type DecisionPublisher interface {
	// PublishDecision publishes a decision event to NATS.
	// The event is serialized as JSON and published to subject
	// "aureliomod.decisions.{workspace_id}".
	// Must be non-blocking: returns immediately, failures are logged.
	PublishDecision(ctx context.Context, event *DecisionEvent) error
}

// NatsConnection is the subset of nats.Conn needed for publishing decisions.
// This interface enables testing without a real NATS connection.
type NatsConnection interface {
	Publish(subject string, data []byte) error
}

// NATSPublisher implements DecisionPublisher using a NATS JetStream
// connection. It publishes decisions as JSON to the subject
// "aureliomod.decisions.{workspace_id}" for Centrifugo relay to the
// dashboard WebSocket.
type NATSPublisher struct {
	conn NatsConnection
}

// Compile-time interface check.
var _ DecisionPublisher = (*NATSPublisher)(nil)

// NewNATSPublisher creates a NATSPublisher backed by the given NATS connection.
// The conn can be a *nats.Client or any NatsConnection implementation.
func NewNATSPublisher(conn NatsConnection) *NATSPublisher {
	return &NATSPublisher{conn: conn}
}

// PublishDecision serializes the decision event as JSON and publishes it
// to the subject "aureliomod.decisions.{workspace_id}".
//
// This is fire-and-forget: if NATS is unavailable, a warning is logged
// but no error is returned to the caller. The pipeline must never be
// blocked by NATS availability.
func (p *NATSPublisher) PublishDecision(ctx context.Context, event *DecisionEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		// Marshal failures indicate a programming error (invalid data)
		return fmt.Errorf("nats publisher marshal: %w", err)
	}

	subject := fmt.Sprintf("aureliomod.decisions.%s", event.WorkspaceID)

	if err := p.conn.Publish(subject, data); err != nil {
		// NATS unavailable — log warning but do NOT return error.
		// The pipeline must continue regardless of dashboard availability.
		slog.WarnContext(ctx, "nats publish failed (non-fatal)",
			slog.String("subject", subject),
			slog.String("decision_id", event.DecisionID),
			slog.String("error", err.Error()),
		)
	}

	return nil
}
