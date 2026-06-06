package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestSlogEmitter_Emit_ValidJSON verifies that the SlogEmitter writes a
// valid JSON line to stdout with all required fields for a BLOCK event.
func TestSlogEmitter_Emit_ValidJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	emitter := NewSlogEmitter(logger)

	event := AuditEvent{
		WorkspaceID:    "ws_alpha",
		ContentID:      "msg_12345",
		ContentHash:    "b3:deadbeefcafebabe",
		Decision:       "BLOCK",
		BlockReason:    "violence_graphic",
		Category:       "violence_graphic",
		AnalystVersion: "wavespeed-v3.2",
		GuildID:        "123456789",
		AuthorID:       "987654321",
		SourcePlatform: "discord",
		ElapsedMs:      245,
		TimestampUTC:   time.Date(2026, 6, 6, 15, 0, 0, 0, time.UTC),
	}

	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit() returned error: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("Emit produced no output")
	}

	// Verify all required fields are present in JSON output
	requiredFields := []string{
		`"workspace_id"`,
		`"ws_alpha"`,
		`"content_id"`,
		`"msg_12345"`,
		`"content_hash"`,
		`"b3:deadbeefcafebabe"`,
		`"decision"`,
		`"BLOCK"`,
		`"block_reason"`,
		`"violence_graphic"`,
		`"guild_id"`,
		`"123456789"`,
		`"author_id"`,
		`"987654321"`,
		`"source_platform"`,
		`"discord"`,
		`"elapsed_ms"`,
	}
	for _, field := range requiredFields {
		if !strings.Contains(output, field) {
			t.Errorf("JSON output missing field: %s", field)
		}
	}
}

// TestSlogEmitter_Emit_Unmarshalable verifies the output is valid JSON that
// can be unmarshalled back into a struct.
func TestSlogEmitter_Emit_Unmarshalable(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	emitter := NewSlogEmitter(logger)

	event := AuditEvent{
		WorkspaceID:    "ws_beta",
		ContentID:      "msg_67890",
		ContentHash:    "b3:abc123def456",
		Decision:       "ALLOW",
		GuildID:        "111222333",
		AuthorID:       "444555666",
		SourcePlatform: "discord",
		ElapsedMs:      12,
		TimestampUTC:   time.Now().UTC(),
	}

	err := emitter.Emit(t.Context(), event)
	if err != nil {
		t.Fatalf("Emit() returned error: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("Emit produced no output")
	}

	// Must be valid JSON
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("Output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// Verify key values survive round-trip
	if decoded["workspace_id"] != "ws_beta" {
		t.Errorf("workspace_id mismatch: got %v", decoded["workspace_id"])
	}
	if decoded["decision"] != "ALLOW" {
		t.Errorf("decision mismatch: got %v", decoded["decision"])
	}
}

// TestSlogEmitter_ImplementsEmitter verifies compile-time interface compliance.
func TestSlogEmitter_ImplementsEmitter(t *testing.T) {
	var _ Emitter = NewSlogEmitter(slog.Default())
}

// TestSlogEmitter_Emit_ContextCancelled verifies Emit handles cancelled context.
func TestSlogEmitter_Emit_ContextCancelled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	emitter := NewSlogEmitter(logger)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	event := AuditEvent{
		WorkspaceID:    "ws_gamma",
		ContentID:      "msg_cancelled",
		Decision:       "BLOCK",
		GuildID:        "0",
		AuthorID:       "0",
		SourcePlatform: "discord",
		TimestampUTC:   time.Now().UTC(),
	}

	// SlogEmitter writes to stdout even with cancelled context (no-op test)
	err := emitter.Emit(ctx, event)
	if err != nil {
		t.Logf("Emit with cancelled context returned error (acceptable): %v", err)
	}
}
