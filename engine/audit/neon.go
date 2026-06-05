package audit

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver — registers "postgres" for sql.Open
)

// dbExecutor abstracts database write operations for testability.
// *sql.DB satisfies this interface directly.
type dbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// NeonEmitter writes audit events to Neon DB via direct INSERT.
// It implements both AuditEmitter (standalone use) and NeonStore
// (plugging into MultiEmitter alongside slog and R2).
//
// NeonEmitter is a best-effort, fire-and-forget emitter: Emit and
// InsertAudit always return nil. DB failures are logged as warnings
// via slog and never propagate to callers.
type NeonEmitter struct {
	db      dbExecutor
	table   string
	enabled bool
	logger  *slog.Logger
}

// Compile-time interface compliance.
var (
	_ AuditEmitter = (*NeonEmitter)(nil)
	_ NeonStore    = (*NeonEmitter)(nil)
)

// NewNeonEmitterFromEnv creates a NeonEmitter based on environment variables:
//
//   - NEON_AUDIT_ENABLED=true  → connect using NEON_DATABASE_URL
//   - NEON_AUDIT_ENABLED unset → return a disabled (no-op) emitter
//
// A disabled emitter's Emit/InsertAudit always returns nil without doing
// any work, so callers don't need to check for nil.
func NewNeonEmitterFromEnv() (*NeonEmitter, error) {
	if os.Getenv("NEON_AUDIT_ENABLED") != "true" {
		return &NeonEmitter{enabled: false, logger: slog.Default()}, nil
	}
	url := os.Getenv("NEON_DATABASE_URL")
	if url == "" {
		return nil, fmt.Errorf("NEON_AUDIT_ENABLED=true requires NEON_DATABASE_URL")
	}
	return NewNeonEmitter(context.Background(), url)
}

// NewNeonEmitter connects to a PostgreSQL-compatible database at dbURL
// (e.g., a Neon DB connection string). The connection is verified with a
// ping using a 5-second timeout.
//
// Connection pool defaults: 10 max open, 5 max idle, 5-minute lifetime.
func NewNeonEmitter(ctx context.Context, dbURL string) (*NeonEmitter, error) {
	if dbURL == "" {
		return nil, fmt.Errorf("neon: database URL is empty")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("neon: sql.Open: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctxTimeout); err != nil {
		db.Close()
		return nil, fmt.Errorf("neon: ping: %w", err)
	}

	return &NeonEmitter{
		db:      db,
		table:   "audit_log",
		enabled: true,
		logger:  slog.Default(),
	}, nil
}

// newNeonEmitterForTesting creates a NeonEmitter with an injected dbExecutor
// and logger. Only used within the audit package tests.
func newNeonEmitterForTesting(db dbExecutor, logger *slog.Logger) *NeonEmitter {
	return &NeonEmitter{
		db:      db,
		table:   "audit_log",
		enabled: true,
		logger:  logger,
	}
}

// Emit writes the audit event to Neon DB. Always returns nil — DB failures
// are logged as warnings and never propagate to callers.
//
// Implements AuditEmitter.
func (ne *NeonEmitter) Emit(ctx context.Context, event AuditEvent) error {
	return ne.InsertAudit(ctx, event)
}

// InsertAudit inserts the audit event into the audit_log table using a
// parameterized query. The operation has a 2-second timeout.
//
// Implements NeonStore.
func (ne *NeonEmitter) InsertAudit(ctx context.Context, event AuditEvent) error {
	if !ne.enabled {
		return nil
	}
	if ne.db == nil {
		return nil
	}

	const query = `INSERT INTO audit_log (
		audit_id, workspace_id, content_hash, decision, confidence,
		category, analyst_version, normalization_pipeline, processing_ms, timestamp_utc
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	ctxTimeout, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	_, err := ne.db.ExecContext(ctxTimeout, query,
		event.AuditID,
		event.WorkspaceID,
		event.ContentHash,
		event.Decision,
		event.Confidence,
		event.Category,
		event.AnalystVersion,
		event.NormalizationPipeline,
		event.ProcessingMs,
		event.TimestampUTC,
	)
	if err != nil {
		ne.logger.WarnContext(ctx, "audit: neon insert failed",
			slog.String("audit_id", event.AuditID),
			slog.String("error", err.Error()),
		)
	}

	return nil // fire-and-forget: never return error to caller
}

// Close closes the underlying database connection.
// Safe to call on disabled or nil-db emitters (returns nil).
func (ne *NeonEmitter) Close() error {
	if ne.db == nil {
		return nil
	}
	if closer, ok := ne.db.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
