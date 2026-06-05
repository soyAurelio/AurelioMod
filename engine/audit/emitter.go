package audit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
)

// errSlogFailed is returned when the critical slog write path fails.
var errSlogFailed = errors.New("audit: slog emission failed (critical path)")

// NeonStore defines the async interface for inserting audit records into
// Neon DB. The implementation is a stub until Neon DB is provisioned.
type NeonStore interface {
	// InsertAudit inserts an audit event into the append-only audit_log table.
	// Must be safe for concurrent use (called from goroutines).
	InsertAudit(ctx context.Context, event AuditEvent) error
}

// R2Store defines the async interface for storing audit records as JSON
// objects in Cloudflare R2 (or MinIO for dev). The implementation is a
// stub until R2/MinIO is provisioned.
type R2Store interface {
	// StoreAudit stores an audit event as a JSON object in cold storage.
	// Key format: workspace_id/YYYY/MM/DD/audit_id.json
	// Must be safe for concurrent use (called from goroutines).
	StoreAudit(ctx context.Context, event AuditEvent) error
}

// errorTracker wraps an io.Writer and tracks whether any Write call failed.
// Used for detecting slog write failures on the critical path.
type errorTracker struct {
	w     io.Writer
	err   error
	mu    sync.Mutex
}

func (t *errorTracker) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	t.mu.Lock()
	if err != nil && t.err == nil {
		t.err = err
	}
	t.mu.Unlock()
	return n, err
}

// LastError returns the first write error detected, or nil.
func (t *errorTracker) LastError() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// Reset clears the tracked error for reuse.
func (t *errorTracker) Reset() {
	t.mu.Lock()
	t.err = nil
	t.mu.Unlock()
}

// MultiEmitter writes audit events to three destinations with different
// failure semantics:
//
//  1. slog JSON stdout — blocking, CRITICAL. If this fails, Emit returns
//     an error immediately.
//  2. Neon DB — fire-and-forget goroutine, BEST-EFFORT. Failures are
//     logged as warnings.
//  3. R2/MinIO — fire-and-forget goroutine, BEST-EFFORT. Failures are
//     logged as warnings.
//
// The slog path is synchronous because audit events MUST appear in stdout
// before the response is sent (for VictoriaMetrics ingestion). Neon and
// R2 are asynchronous because they are external I/O and must not add
// latency to the request pipeline.
type MultiEmitter struct {
	logger  *slog.Logger
	tracker *errorTracker // nil if error tracking is not needed
	neon    NeonStore
	r2      R2Store
	wg      sync.WaitGroup // tracks in-flight async writes for graceful shutdown
}

// Compile-time interface check.
var _ AuditEmitter = (*MultiEmitter)(nil)

// NewMultiEmitter creates a MultiEmitter that writes audit events to slog,
// Neon DB, and R2. The NeonStore and R2Store may be nil if those
// destinations are not yet provisioned — in that case they are skipped
// silently.
//
// The slog.Logger's underlying writer is checked for write errors after
// each Emit call. If error tracking is needed (for production or testing),
// wrap the writer with NewErrorTrackingEmitter.
func NewMultiEmitter(logger *slog.Logger, neon NeonStore, r2 R2Store) *MultiEmitter {
	return &MultiEmitter{
		logger: logger,
		neon:   neon,
		r2:     r2,
	}
}

// NewMultiEmitterWithTracker creates a MultiEmitter with error tracking on
// the underlying slog writer. The tracker wraps the given writer and
// detects I/O write failures on the slog critical path.
func NewMultiEmitterWithTracker(w io.Writer, handlerOpts *slog.HandlerOptions, neon NeonStore, r2 R2Store) *MultiEmitter {
	tracker := &errorTracker{w: w}
	logger := slog.New(slog.NewJSONHandler(tracker, handlerOpts))
	return &MultiEmitter{
		logger:  logger,
		tracker: tracker,
		neon:    neon,
		r2:      r2,
	}
}

// Emit writes the audit event to all configured destinations.
//
// slog is written synchronously — if the underlying writer fails, Emit
// returns errSlogFailed immediately.
//
// Neon DB and R2 are launched in fire-and-forget goroutines. Failures are
// logged via slog.WarnContext and do NOT affect the return value.
func (e *MultiEmitter) Emit(ctx context.Context, event AuditEvent) error {
	// 1. slog — blocking, critical path
	if e.tracker != nil {
		e.tracker.Reset()
	}

	e.logger.InfoContext(ctx, "audit_event",
		slog.String("audit_id", event.AuditID),
		slog.String("workspace_id", event.WorkspaceID),
		slog.String("content_hash", event.ContentHash),
		slog.String("decision", event.Decision),
		slog.Float64("confidence", event.Confidence),
		slog.String("category", event.Category),
		slog.String("analyst_version", event.AnalystVersion),
		slog.String("normalization_pipeline", event.NormalizationPipeline),
		slog.Int64("processing_ms", event.ProcessingMs),
		slog.Time("timestamp_utc", event.TimestampUTC),
	)

	// Check if the underlying writer had an error
	if e.tracker != nil {
		if err := e.tracker.LastError(); err != nil {
			return errSlogFailed
		}
	}

	// 2. Neon DB — fire-and-forget goroutine (best-effort)
	if e.neon != nil {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			if err := e.neon.InsertAudit(ctx, event); err != nil {
				slog.WarnContext(ctx, "audit: neon insert failed",
					slog.String("audit_id", event.AuditID),
					slog.String("error", err.Error()),
				)
			}
		}()
	}

	// 3. R2/MinIO — fire-and-forget goroutine (best-effort)
	if e.r2 != nil {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			if err := e.r2.StoreAudit(ctx, event); err != nil {
				slog.WarnContext(ctx, "audit: r2 store failed",
					slog.String("audit_id", event.AuditID),
					slog.String("error", err.Error()),
				)
			}
		}()
	}

	return nil
}

// Wait blocks until all in-flight async audit writes have completed.
// Useful for graceful shutdown and testing.
func (e *MultiEmitter) Wait() {
	e.wg.Wait()
}
