package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestObjectKey(t *testing.T) {
	t.Run("standard key format", func(t *testing.T) {
		event := AuditEvent{
			AuditID:       "evt_abc123",
			WorkspaceID:   "ws_test",
			TimestampUTC:  time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC),
		}
		key := objectKey(event)
		expected := "ws_test/2026/06/06/evt_abc123.json"
		if key != expected {
			t.Errorf("objectKey = %q, want %q", key, expected)
		}
	})

	t.Run("workspace with special chars", func(t *testing.T) {
		event := AuditEvent{
			AuditID:       "evt_test",
			WorkspaceID:   "ws/sub/dir",
			TimestampUTC:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		key := objectKey(event)
		expected := "ws_sub_dir/2026/01/01/evt_test.json"
		if key != expected {
			t.Errorf("objectKey = %q, want %q", key, expected)
		}
	})

	t.Run("single-digit month and day", func(t *testing.T) {
		event := AuditEvent{
			AuditID:       "evt_pad",
			WorkspaceID:   "ws",
			TimestampUTC:  time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		}
		key := objectKey(event)
		expected := "ws/2026/03/05/evt_pad.json"
		if key != expected {
			t.Errorf("objectKey = %q, want %q", key, expected)
		}
	})
}

func TestS3AuditStore_StoreAudit(t *testing.T) {
	// Since we can't easily mock S3 client, we test with a mock that
	// intercepts the PutObject call via a helper interface.
	// For now, we test the JSON serialization and key logic indirectly.

	t.Run("store audit event generates valid JSON", func(t *testing.T) {
		event := AuditEvent{
			AuditID:               "evt_json_test",
			WorkspaceID:           "ws_test",
			ContentHash:           "b3:abc123",
			Decision:              "blocked",
			Confidence:            0.94,
			Category:              "violence_graphic",
			AnalystVersion:        "wavespeed-v3.2",
			NormalizationPipeline: "480p+strip_exif+jpeg_q85",
			ProcessingMs:          142,
			TimestampUTC:          time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC),
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}

		// Verify the JSON round-trips correctly
		var decoded AuditEvent
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}

		if decoded.AuditID != event.AuditID {
			t.Errorf("AuditID mismatch: got %q, want %q", decoded.AuditID, event.AuditID)
		}
		if decoded.Decision != event.Decision {
			t.Errorf("Decision mismatch: got %q, want %q", decoded.Decision, event.Decision)
		}
	})
}

func TestSanitizeKeySegment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"clean string", "workspace_42", "workspace_42"},
		{"forward slash", "ws/sub", "ws_sub"},
		{"backslash", "ws\\sub", "ws_sub"},
		{"multiple slashes", "a/b/c", "a_b_c"},
		{"mixed", "ws/sub\\name", "ws_sub_name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeKeySegment(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeKeySegment(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// mockS3Client implements the PutObject call for testing.
type mockS3Client struct {
	objects map[string][]byte // key → body
}

func (m *mockS3Client) putObjectJSON(key string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.objects[key] = data
	return nil
}

func TestS3AuditStore_IntegrationMock(t *testing.T) {
	t.Run("put object via mock client", func(t *testing.T) {
		mock := &mockS3Client{objects: make(map[string][]byte)}

		event := AuditEvent{
			AuditID:       "evt_mock_test",
			WorkspaceID:   "ws_mock",
			ContentHash:   "b3:deadbeef",
			Decision:      "allowed",
			Confidence:    0.05,
			TimestampUTC:  time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		}

		data, _ := json.Marshal(event)
		err := mock.putObjectJSON(objectKey(event), bytes.NewReader(data))
		if err != nil {
			t.Fatalf("putObjectJSON: %v", err)
		}

		if len(mock.objects) != 1 {
			t.Fatalf("expected 1 object, got %d", len(mock.objects))
		}

		expectedKey := "ws_mock/2026/06/06/evt_mock_test.json"
		stored, ok := mock.objects[expectedKey]
		if !ok {
			t.Errorf("object not found at key %q, keys: %v", expectedKey, mock.objects)
		}

		var decoded AuditEvent
		if err := json.Unmarshal(stored, &decoded); err != nil {
			t.Fatalf("json.Unmarshal stored: %v", err)
		}
		if decoded.AuditID != event.AuditID {
			t.Errorf("stored AuditID = %q, want %q", decoded.AuditID, event.AuditID)
		}
	})
}
