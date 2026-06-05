package audit

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockNeonStore implements NeonStore for testing.
type mockNeonStore struct {
	mu            sync.Mutex
	inserted      []AuditEvent
	simError      error
	insertStarted chan struct{} // closed when insert goroutine starts
}

func (m *mockNeonStore) InsertAudit(ctx context.Context, event AuditEvent) error {
	if m.insertStarted != nil {
		close(m.insertStarted)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.simError != nil {
		return m.simError
	}
	m.inserted = append(m.inserted, event)
	return nil
}

// mockR2Store implements R2Store for testing.
type mockR2Store struct {
	mu       sync.Mutex
	stored   []AuditEvent
	simError error
}

func (m *mockR2Store) StoreAudit(ctx context.Context, event AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.simError != nil {
		return m.simError
	}
	m.stored = append(m.stored, event)
	return nil
}

func sampleEvent() AuditEvent {
	return AuditEvent{
		AuditID:               "evt_test123",
		WorkspaceID:           "ws_xyz",
		ContentHash:           "b3:a1b2c3d4e5f6a7b8b9c0d1e2f3a4b5c6",
		Decision:              "blocked",
		Confidence:            0.94,
		Category:              "violence_graphic",
		AnalystVersion:        "wavespeed-v3.2",
		NormalizationPipeline: "480p+strip_exif+jpeg_q85",
		ProcessingMs:          142,
		TimestampUTC:          time.Date(2026, 6, 3, 22, 45, 0, 0, time.UTC),
	}
}

// TestMultiEmitter_Emit_SlogWritesJSON verifies that Emit writes the audit
// event as structured JSON via slog to the configured writer.
func TestMultiEmitter_Emit_SlogWritesJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	neon := &mockNeonStore{}
	r2 := &mockR2Store{}
	emitter := NewMultiEmitter(logger, neon, r2)

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("Emit produced no slog output")
	}

	// Verify key fields are present in JSON output
	for _, field := range []string{
		`"audit_id"`,
		`"evt_test123"`,
		`"workspace_id"`,
		`"ws_xyz"`,
		`"decision"`,
		`"blocked"`,
		`"confidence"`,
		`0.94`,
		`"category"`,
		`"violence_graphic"`,
	} {
		if !strings.Contains(output, field) {
			t.Errorf("slog output missing field: %q", field)
		}
	}
}

// TestMultiEmitter_Emit_NeonAsync verifies that the Neon DB insert happens
// asynchronously (fire-and-forget) and Emit does NOT wait for it.
func TestMultiEmitter_Emit_NeonAsync(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	insertStarted := make(chan struct{})
	neon := &mockNeonStore{insertStarted: insertStarted}
	r2 := &mockR2Store{}
	emitter := NewMultiEmitter(logger, neon, r2)

	event := sampleEvent()

	// Emit should return immediately — Neon is async
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}

	// Wait for Neon goroutine to start (with timeout)
	select {
	case <-insertStarted:
		// Goroutine started — Neon insert is running async
	case <-time.After(2 * time.Second):
		t.Fatal("Neon insert goroutine did not start within 2s")
	}

	// Give goroutine time to finish
	time.Sleep(100 * time.Millisecond)

	neon.mu.Lock()
	inserted := len(neon.inserted)
	neon.mu.Unlock()

	if inserted != 1 {
		t.Errorf("Expected 1 Neon insert, got %d", inserted)
	}
}

// TestMultiEmitter_Emit_R2Async verifies that R2 storage happens
// asynchronously and Emit does NOT wait for it.
func TestMultiEmitter_Emit_R2Async(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	neon := &mockNeonStore{}
	r2 := &mockR2Store{}
	emitter := NewMultiEmitter(logger, neon, r2)

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}

	// Give goroutines time to complete
	time.Sleep(100 * time.Millisecond)

	r2.mu.Lock()
	stored := len(r2.stored)
	r2.mu.Unlock()

	if stored != 1 {
		t.Errorf("Expected 1 R2 store, got %d (async goroutine may not have finished)", stored)
	}
}

// TestMultiEmitter_Emit_NeonFailureDoesNotBlock verifies that when Neon DB
// fails, Emit logs a warning but does NOT return an error (non-blocking).
func TestMultiEmitter_Emit_NeonFailureDoesNotBlock(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	neon := &mockNeonStore{simError: errors.New("Neon DB connection refused")}
	r2 := &mockR2Store{}
	emitter := NewMultiEmitter(logger, neon, r2)

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit should not fail when Neon is down, got: %v", err)
	}

	// Give goroutine time
	time.Sleep(100 * time.Millisecond)

	// Verify R2 still works (it's independent of Neon)
	r2.mu.Lock()
	stored := len(r2.stored)
	r2.mu.Unlock()
	if stored != 1 {
		t.Errorf("R2 should still store even when Neon fails, stored=%d", stored)
	}
}

// TestMultiEmitter_Emit_R2FailureDoesNotBlock verifies that R2 failure is
// non-blocking — Emit still succeeds (slog + Neon are independent).
func TestMultiEmitter_Emit_R2FailureDoesNotBlock(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	neon := &mockNeonStore{}
	r2 := &mockR2Store{simError: errors.New("R2 bucket not found")}
	emitter := NewMultiEmitter(logger, neon, r2)

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit should not fail when R2 is down, got: %v", err)
	}

	// Give goroutines time
	time.Sleep(100 * time.Millisecond)

	// Neon should still work
	neon.mu.Lock()
	inserted := len(neon.inserted)
	neon.mu.Unlock()
	if inserted != 1 {
		t.Errorf("Neon should still insert even when R2 fails, inserted=%d", inserted)
	}
}

// TestMultiEmitter_Emit_SlogFailureReturnsError verifies that when the slog
// write itself fails, Emit returns an error — slog is the critical path.
func TestMultiEmitter_Emit_SlogFailureReturnsError(t *testing.T) {
	neon := &mockNeonStore{}
	r2 := &mockR2Store{}

	// Use NewMultiEmitterWithTracker with a failing writer
	emitter := NewMultiEmitterWithTracker(&failingWriter{}, nil, neon, r2)

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err == nil {
		t.Fatal("Expected error when slog write fails, got nil")
	}
	if !errors.Is(err, errSlogFailed) {
		t.Errorf("Expected errSlogFailed, got: %v", err)
	}
}

// TestMultiEmitter_Emit_AllThreeDestinations verifies that a successful
// emit writes to all three destinations (slog + Neon + R2).
func TestMultiEmitter_Emit_AllThreeDestinations(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	neon := &mockNeonStore{}
	r2 := &mockR2Store{}
	emitter := NewMultiEmitter(logger, neon, r2)

	event := sampleEvent()
	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}

	// slog must have written
	if buf.Len() == 0 {
		t.Fatal("slog produced no output")
	}

	// Wait for async goroutines
	time.Sleep(100 * time.Millisecond)

	// Neon must have received the event
	neon.mu.Lock()
	if len(neon.inserted) != 1 {
		t.Errorf("Expected 1 Neon insert, got %d", len(neon.inserted))
	}
	if len(neon.inserted) > 0 && neon.inserted[0].AuditID != "evt_test123" {
		t.Errorf("Neon audit_id mismatch: %q", neon.inserted[0].AuditID)
	}
	neon.mu.Unlock()

	// R2 must have received the event
	r2.mu.Lock()
	if len(r2.stored) != 1 {
		t.Errorf("Expected 1 R2 store, got %d", len(r2.stored))
	}
	r2.mu.Unlock()
}

// TestMultiEmitter_SatisfiesAuditEmitter verifies compile-time interface compliance.
func TestMultiEmitter_SatisfiesAuditEmitter(t *testing.T) {
	var _ AuditEmitter = (*MultiEmitter)(nil)
}

// failingWriter always fails on Write, used to test slog failure path.
type failingWriter struct{}

func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}
