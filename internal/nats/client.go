// Package nats provides NATS JetStream messaging for the AurelioMod pipeline.
// Edge services publish content analysis jobs; Engine services consume them.
//
// Authentication:
//   - Phase 1 (dev): user:pass in URL (nats://user:pass@host:4222)
//   - Phase 2 (prod): nkey seed via NATS_NKEY_SEED env var or NkeySeed config field
package nats

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/nats-io/nats.go"
)

// Client wraps a NATS connection with JetStream support.
type Client struct {
	conn *nats.Conn
	js   nats.JetStreamContext
}

// Config holds NATS connection parameters.
type Config struct {
	URL      string
	NkeySeed string // nkey private seed (replaces user:pass; Fase 2+)
	LogLevel slog.Level
}

// DefaultConfig returns sensible defaults for development.
func DefaultConfig() Config {
	return Config{
		URL:      "nats://localhost:4222",
		LogLevel: slog.LevelInfo,
	}
}

// Connect establishes a persistent connection to NATS with auto-reconnect.
// Uses a 5-second timeout so the caller doesn't block indefinitely.
//
// Auth precedence: NkeySeed config > NATS_NKEY_SEED env var > user:pass in URL.
func Connect(cfg Config) (*Client, error) {
	// Resolve nkey seed — config field takes precedence over env var
	nkeySeed := cfg.NkeySeed
	if nkeySeed == "" {
		nkeySeed = os.Getenv("NATS_NKEY_SEED")
	}

	opts := []nats.Option{
		nats.Name("aureliomod"),
		nats.Timeout(5 * time.Second),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("nats disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.Info("nats reconnected")
		}),
	}

	// Nkey auth (Fase 2+) — stronger than user:pass
	if nkeySeed != "" {
		nkeyOpt, err := nats.NkeyOptionFromSeed(nkeySeed)
		if err != nil {
			return nil, fmt.Errorf("nats nkey: %w", err)
		}
		opts = append(opts, nkeyOpt)
		slog.Info("nats using nkey authentication")
	}

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats jetstream: %w", err)
	}

	slog.Info("nats connected", "url", cfg.URL)
	return &Client{conn: nc, js: js}, nil
}

// PublishAnalysisJob publishes a content analysis job to the JetStream stream.
func (c *Client) PublishAnalysisJob(ctx context.Context, subject string, data []byte) error {
	_, err := c.js.Publish(subject, data)
	return err
}

// SubscribeAnalysisJobs creates a durable consumer for content analysis jobs.
func (c *Client) SubscribeAnalysisJobs(ctx context.Context, subject, durable string, handler func(msg []byte) error) error {
	_, err := c.js.Subscribe(subject, func(msg *nats.Msg) {
		if err := handler(msg.Data); err != nil {
			slog.Error("analysis job failed", "subject", msg.Subject, "error", err)
			msg.Nak()
			return
		}
		msg.Ack()
	}, nats.Durable(durable), nats.ManualAck())
	return err
}

// Conn returns the underlying NATS connection.
func (c *Client) Conn() *nats.Conn {
	return c.conn
}

// JetStream returns the JetStream context.
func (c *Client) JetStream() nats.JetStreamContext {
	return c.js
}

// Close drains and closes the NATS connection.
func (c *Client) Close() {
	c.conn.Drain()
}
