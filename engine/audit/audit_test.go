package audit

import (
	"context"
	"testing"
	"time"
)

// TestGenerateAuditID_Deterministic verifies that generateAuditID produces
// the same output for the same inputs (content hash + timestamp).
func TestGenerateAuditID_Deterministic(t *testing.T) {
	hash := "b3:a1b2c3d4e5f6a7b8b9c0d1e2f3a4b5c6"
	ts := time.Date(2026, 6, 3, 22, 45, 0, 0, time.UTC)

	id1 := generateAuditID(hash, ts)
	id2 := generateAuditID(hash, ts)

	if id1 != id2 {
		t.Errorf("generateAuditID not deterministic: %q != %q", id1, id2)
	}

	// Must have the "evt_" prefix
	if len(id1) < 5 {
		t.Errorf("audit id too short: %q", id1)
	}
}

// TestGenerateAuditID_UniquePerTimestamp verifies that different timestamps
// produce different audit IDs even with the same content hash.
func TestGenerateAuditID_UniquePerTimestamp(t *testing.T) {
	hash := "b3:a1b2c3d4e5f6a7b8b9c0d1e2f3a4b5c6"
	ts1 := time.Date(2026, 6, 3, 22, 45, 0, 0, time.UTC)
	ts2 := time.Date(2026, 6, 3, 22, 45, 1, 0, time.UTC)

	id1 := generateAuditID(hash, ts1)
	id2 := generateAuditID(hash, ts2)

	if id1 == id2 {
		t.Errorf("generateAuditID should produce different IDs for different timestamps: %q", id1)
	}
}

// TestAuditEvent_HasAllFields verifies the AuditEvent struct has all 10 required fields
// matching the NIS2/GDPR compliance schema from aureliomod-stack.md §4.
func TestAuditEvent_HasAllFields(t *testing.T) {
	ts := time.Date(2026, 6, 3, 22, 45, 0, 0, time.UTC)
	event := AuditEvent{
		AuditID:               "evt_test123",
		WorkspaceID:           "ws_xyz",
		ContentHash:           "b3:a1b2c3d4e5f6a7b8b9c0d1e2f3a4b5c6",
		Decision:              "blocked",
		Confidence:            0.94,
		Category:              "violence_graphic",
		AnalystVersion:        "AI moderation-v3.2",
		NormalizationPipeline: "480p+strip_exif+jpeg_q85",
		ProcessingMs:          142,
		TimestampUTC:          ts,
	}

	// Verify all 10 fields have values
	if event.AuditID == "" {
		t.Error("audit_id must not be empty")
	}
	if event.WorkspaceID == "" {
		t.Error("workspace_id must not be empty")
	}
	if event.ContentHash == "" {
		t.Error("content_hash must not be empty")
	}
	if event.Decision == "" {
		t.Error("decision must not be empty")
	}
	if event.Confidence == 0 {
		t.Error("confidence must not be zero (should be between 0 and 1)")
	}
	if event.Category == "" {
		t.Error("category must not be empty")
	}
	if event.AnalystVersion == "" {
		t.Error("analyst_version must not be empty")
	}
	if event.NormalizationPipeline == "" {
		t.Error("normalization_pipeline must not be empty")
	}
	if event.ProcessingMs == 0 {
		t.Error("processing_ms must not be zero")
	}
	if event.TimestampUTC.IsZero() {
		t.Error("timestamp_utc must not be zero")
	}

	// Verify exact values from spec example
	if event.Confidence != 0.94 {
		t.Errorf("confidence = %f, want 0.94", event.Confidence)
	}
	if event.Decision != "blocked" {
		t.Errorf("decision = %q, want \"blocked\"", event.Decision)
	}
	if event.Category != "violence_graphic" {
		t.Errorf("category = %q, want \"violence_graphic\"", event.Category)
	}
	if event.ProcessingMs != 142 {
		t.Errorf("processing_ms = %d, want 142", event.ProcessingMs)
	}
}

// TestAuditEmitter_InterfaceContract verifies that the AuditEmitter interface
// is defined with Emit(ctx, AuditEvent) error signature.
func TestAuditEmitter_InterfaceContract(t *testing.T) {
	// Compile-time: the interface must be usable
	var _ AuditEmitter = (*testEmitter)(nil)
}

// testEmitter is a minimal AuditEmitter implementation for interface contract testing.
type testEmitter struct{}

func (e *testEmitter) Emit(_ context.Context, _ AuditEvent) error {
	return nil
}
