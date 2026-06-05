package audit

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// --- mockDBExecutor ---

// mockDBExecutor implements dbExecutor for unit testing. It records every
// ExecContext call and can simulate errors on demand.
type mockDBExecutor struct {
	mu       sync.Mutex
	execs    []execCall
	simError error
}

type execCall struct {
	query string
	args  []any
}

func (m *mockDBExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.execs = append(m.execs, execCall{query: query, args: args})
	if m.simError != nil {
		return nil, m.simError
	}
	return nil, nil
}

// execCount returns the number of recorded ExecContext calls. Thread-safe.
func (m *mockDBExecutor) execCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.execs)
}

// lastExec returns the last recorded exec call, or an empty execCall.
// Thread-safe.
func (m *mockDBExecutor) lastExec() execCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.execs) == 0 {
		return execCall{}
	}
	return m.execs[len(m.execs)-1]
}

// --- test helpers ---

// testLogger creates a slog.Logger that writes to the given buffer at WARN
// level, used for capturing warning output in tests.
func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// --- tests: Emit / InsertAudit ---

// TestNeonEmitter_Emit_SuccessfulInsert verifies that Emit calls ExecContext
// with the correct parameterized query and all audit event fields.
func TestNeonEmitter_Emit_SuccessfulInsert(t *testing.T) {
	mock := &mockDBExecutor{}
	emitter := newNeonEmitterForTesting(mock, testLogger(new(bytes.Buffer)))

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit returned unexpected error: %v", err)
	}

	if count := mock.execCount(); count != 1 {
		t.Fatalf("expected 1 ExecContext call, got %d", count)
	}

	call := mock.lastExec()

	// Verify query contains the table name (loose check)
	if !strings.Contains(call.query, "INSERT INTO audit_log") {
		t.Errorf("query missing table name: %s", call.query)
	}

	// Verify positional parameters match event fields
	if len(call.args) != 10 {
		t.Fatalf("expected 10 positional args, got %d", len(call.args))
	}

	assertArg(t, call.args, 0, event.AuditID, "audit_id")
	assertArg(t, call.args, 1, event.WorkspaceID, "workspace_id")
	assertArg(t, call.args, 2, event.ContentHash, "content_hash")
	assertArg(t, call.args, 3, event.Decision, "decision")
	assertArg(t, call.args, 4, event.Confidence, "confidence")
	assertArg(t, call.args, 5, event.Category, "category")
	assertArg(t, call.args, 6, event.AnalystVersion, "analyst_version")
	assertArg(t, call.args, 7, event.NormalizationPipeline, "normalization_pipeline")
	assertArg(t, call.args, 8, event.ProcessingMs, "processing_ms")
	assertArg(t, call.args, 9, event.TimestampUTC, "timestamp_utc")
}

// TestNeonEmitter_Emit_DisabledGate verifies that a disabled emitter is a
// no-op: Emit returns nil and ExecContext is never called.
func TestNeonEmitter_Emit_DisabledGate(t *testing.T) {
	mock := &mockDBExecutor{}

	emitter := &NeonEmitter{
		db:      mock,
		table:   "audit_log",
		enabled: false,
		logger:  testLogger(new(bytes.Buffer)),
	}

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("disabled emitter returned unexpected error: %v", err)
	}

	if count := mock.execCount(); count != 0 {
		t.Errorf("disabled emitter should not call ExecContext, got %d calls", count)
	}
}

// TestNeonEmitter_Emit_NilDB verifies that a nil db is handled gracefully:
// Emit returns nil without panicking.
func TestNeonEmitter_Emit_NilDB(t *testing.T) {
	emitter := &NeonEmitter{
		db:      nil,
		table:   "audit_log",
		enabled: true,
		logger:  testLogger(new(bytes.Buffer)),
	}

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("nil-db emitter returned unexpected error: %v", err)
	}
}

// TestNeonEmitter_Emit_DBUnavailable verifies that when the DB returns an
// error, Emit returns nil (fire-and-forget) and logs a WARNING via slog.
func TestNeonEmitter_Emit_DBUnavailable(t *testing.T) {
	var buf bytes.Buffer
	logger := testLogger(&buf)

	dbErr := errors.New("connection refused")
	mock := &mockDBExecutor{simError: dbErr}
	emitter := newNeonEmitterForTesting(mock, logger)

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit should return nil on DB error (fire-and-forget), got: %v", err)
	}

	// Verify ExecContext was attempted
	if count := mock.execCount(); count != 1 {
		t.Fatalf("expected 1 ExecContext attempt, got %d", count)
	}

	// Verify WARNING was logged
	output := buf.String()
	if output == "" {
		t.Fatal("expected warning log on DB error, got empty output")
	}
	if !strings.Contains(output, "neon insert failed") {
		t.Errorf("warning log missing 'neon insert failed': %s", output)
	}
	if !strings.Contains(output, event.AuditID) {
		t.Errorf("warning log missing audit_id: %s", output)
	}
	if !strings.Contains(output, dbErr.Error()) {
		t.Errorf("warning log missing DB error: %s", output)
	}
}

// TestNeonEmitter_InsertAudit_Interface verifies that InsertAudit works the
// same as Emit (both delegate to the same logic) and returns nil on success.
func TestNeonEmitter_InsertAudit_Interface(t *testing.T) {
	mock := &mockDBExecutor{}
	emitter := newNeonEmitterForTesting(mock, testLogger(new(bytes.Buffer)))

	event := sampleEvent()
	err := emitter.InsertAudit(t.Context(), event)
	if err != nil {
		t.Fatalf("InsertAudit returned unexpected error: %v", err)
	}

	if count := mock.execCount(); count != 1 {
		t.Fatalf("expected 1 ExecContext call, got %d", count)
	}
}

// TestNeonEmitter_InsertAudit_DBErrorReturnsNil verifies that InsertAudit
// returns nil even when the DB is down (implements NeonStore contract for
// fire-and-forget semantics).
func TestNeonEmitter_InsertAudit_DBErrorReturnsNil(t *testing.T) {
	mock := &mockDBExecutor{simError: errors.New("timeout")}
	emitter := newNeonEmitterForTesting(mock, testLogger(new(bytes.Buffer)))

	event := sampleEvent()
	err := emitter.InsertAudit(t.Context(), event)
	if err != nil {
		t.Fatalf("InsertAudit should return nil on DB error, got: %v", err)
	}
}

// --- tests: NewNeonEmitterFromEnv ---

// TestNewNeonEmitterFromEnv_Disabled verifies that when NEON_AUDIT_ENABLED is
// not "true", NewNeonEmitterFromEnv returns a disabled, no-op emitter.
func TestNewNeonEmitterFromEnv_Disabled(t *testing.T) {
	os.Setenv("NEON_AUDIT_ENABLED", "false")
	defer os.Unsetenv("NEON_AUDIT_ENABLED")

	ne, err := NewNeonEmitterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ne == nil {
		t.Fatal("expected non-nil disabled emitter")
	}
	if ne.enabled {
		t.Error("expected disabled emitter when NEON_AUDIT_ENABLED != true")
	}

	// Verify it's truly a no-op
	event := sampleEvent()
	if err := ne.Emit(t.Context(), event); err != nil {
		t.Errorf("disabled emitter should return nil, got: %v", err)
	}
}

// TestNewNeonEmitterFromEnv_MissingURL verifies that setting
// NEON_AUDIT_ENABLED=true without NEON_DATABASE_URL returns an error.
func TestNewNeonEmitterFromEnv_MissingURL(t *testing.T) {
	os.Setenv("NEON_AUDIT_ENABLED", "true")
	os.Unsetenv("NEON_DATABASE_URL")
	defer os.Unsetenv("NEON_AUDIT_ENABLED")

	_, err := NewNeonEmitterFromEnv()
	if err == nil {
		t.Fatal("expected error when NEON_DATABASE_URL is missing")
	}
}

// --- tests: Close ---

// TestNeonEmitter_Close_NilDB verifies Close is safe on a nil-db emitter.
func TestNeonEmitter_Close_NilDB(t *testing.T) {
	ne := &NeonEmitter{db: nil}
	if err := ne.Close(); err != nil {
		t.Errorf("Close on nil db should not error: %v", err)
	}
}

// TestNeonEmitter_Close_DisabledEmitter verifies Close is safe on a
// disabled emitter (no-op).
func TestNeonEmitter_Close_DisabledEmitter(t *testing.T) {
	ne := &NeonEmitter{db: nil, enabled: false}
	if err := ne.Close(); err != nil {
		t.Errorf("Close on disabled emitter should not error: %v", err)
	}
}

// --- tests: interface compliance (compile-time) ---

// TestNeonEmitter_SatisfiesAuditEmitter verifies that NeonEmitter implements
// the AuditEmitter interface.
func TestNeonEmitter_SatisfiesAuditEmitter(t *testing.T) {
	var _ AuditEmitter = (*NeonEmitter)(nil)
}

// TestNeonEmitter_SatisfiesNeonStore verifies that NeonEmitter implements
// the NeonStore interface.
func TestNeonEmitter_SatisfiesNeonStore(t *testing.T) {
	var _ NeonStore = (*NeonEmitter)(nil)
}

// --- helpers ---

// assertArg checks that a positional argument matches the expected value.
func assertArg(t *testing.T, args []any, pos int, expected any, fieldName string) {
	t.Helper()
	if pos >= len(args) {
		t.Fatalf("arg[%d] (%s) missing — only %d args", pos, fieldName, len(args))
	}
	if args[pos] != expected {
		t.Errorf("arg[%d] (%s) = %v, want %v", pos, fieldName, args[pos], expected)
	}
}

// --- integration: emitter with MultiEmitter wiring ---

// TestNeonEmitter_WithMultiEmitter verifies that NeonEmitter can be used as
// a NeonStore within MultiEmitter (real integration without mocking MultiEmitter).
func TestNeonEmitter_WithMultiEmitter(t *testing.T) {
	mock := &mockDBExecutor{}

	ne := &NeonEmitter{
		db:      mock,
		table:   "audit_log",
		enabled: true,
		logger:  testLogger(new(bytes.Buffer)),
	}

	// NeonEmitter as NeonStore must have InsertAudit return nil on success
	event := sampleEvent()
	err := ne.InsertAudit(t.Context(), event)
	if err != nil {
		t.Fatalf("InsertAudit failed: %v", err)
	}

	if count := mock.execCount(); count != 1 {
		t.Fatalf("expected 1 ExecContext call, got %d", count)
	}

	call := mock.lastExec()
	if !strings.Contains(call.query, "INSERT INTO audit_log") {
		t.Errorf("query malformed: %s", call.query)
	}
}

// --- benchmarks ---

// BenchmarkNeonEmitter_Emit measures Emit throughput with a no-op mock DB.
func BenchmarkNeonEmitter_Emit(b *testing.B) {
	mock := &mockDBExecutor{}
	emitter := newNeonEmitterForTesting(mock, testLogger(new(bytes.Buffer)))
	event := sampleEvent()
	ctx := context.Background()

	for b.Loop() {
		_ = emitter.Emit(ctx, event)
	}
}

// sampleEvent is defined in emitter_test.go (same package).
// This file reuses it for consistent test data across audit tests.
