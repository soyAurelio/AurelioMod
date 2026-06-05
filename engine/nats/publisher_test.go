package nats

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- mocks ---

// mockNATSConn implements NatsConnection for testing.
type mockNATSConn struct {
	mu        sync.Mutex
	published []publishedMsg
	simError  error
}

type publishedMsg struct {
	subject string
	data    []byte
}

func (m *mockNATSConn) Publish(subject string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.simError != nil {
		return m.simError
	}
	m.published = append(m.published, publishedMsg{subject: subject, data: data})
	return nil
}

// --- helpers ---

func sampleDecision() *DecisionEvent {
	return &DecisionEvent{
		DecisionID:  "d-12345",
		WorkspaceID: "ws_abc",
		ContentHash: "b3:deadbeefcafebabe",
		Decision:    "blocked",
		Category:    "violence_graphic",
		Confidence:  0.93,
		Timestamp:   time.Date(2026, 6, 3, 22, 45, 0, 0, time.UTC),
	}
}

// --- tests ---

// TestNATSPublisher_PublishDecision verifies that PublishDecision publishes
// a JSON-encoded DecisionEvent to the correct NATS subject.
func TestNATSPublisher_PublishDecision(t *testing.T) {
	conn := &mockNATSConn{}
	pub := NewNATSPublisher(conn)

	event := sampleDecision()
	err := pub.PublishDecision(t.Context(), event)
	if err != nil {
		t.Fatalf("PublishDecision error: %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	if len(conn.published) != 1 {
		t.Fatalf("Expected 1 published message, got %d", len(conn.published))
	}

	msg := conn.published[0]
	expectedSubject := "aureliomod.decisions.ws_abc"
	if msg.subject != expectedSubject {
		t.Errorf("Subject = %q, want %q", msg.subject, expectedSubject)
	}

	// Verify JSON payload
	var decoded DecisionEvent
	if err := json.Unmarshal(msg.data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal published JSON: %v", err)
	}
	if decoded.DecisionID != "d-12345" {
		t.Errorf("DecisionID = %q, want \"d-12345\"", decoded.DecisionID)
	}
	if decoded.Decision != "blocked" {
		t.Errorf("Decision = %q, want \"blocked\"", decoded.Decision)
	}
	if decoded.WorkspaceID != "ws_abc" {
		t.Errorf("WorkspaceID = %q, want \"ws_abc\"", decoded.WorkspaceID)
	}
}

// TestNATSPublisher_PublishDecision_NonBlocking verifies that PublishDecision
// does NOT block — it's fire-and-forget.
func TestNATSPublisher_PublishDecision_NonBlocking(t *testing.T) {
	conn := &mockNATSConn{}
	pub := NewNATSPublisher(conn)

	event := sampleDecision()

	done := make(chan struct{})
	go func() {
		defer close(done)
		err := pub.PublishDecision(t.Context(), event)
		if err != nil {
			t.Errorf("PublishDecision error: %v", err)
		}
	}()

	// Must complete quickly — no blocking
	select {
	case <-done:
		// success — non-blocking
	case <-time.After(2 * time.Second):
		t.Fatal("PublishDecision blocked for >2s, expected non-blocking")
	}
}

// TestNATSPublisher_PublishDecision_NATSUnavailable verifies that when NATS
// is unavailable, PublishDecision logs a warning but does NOT return an error
// (fire-and-forget, best-effort).
func TestNATSPublisher_PublishDecision_NATSUnavailable(t *testing.T) {
	conn := &mockNATSConn{
		simError: errors.New("nats: no connection available"),
	}
	pub := NewNATSPublisher(conn)

	event := sampleDecision()
	err := pub.PublishDecision(t.Context(), event)

	// Should NOT return error — fire-and-forget
	if err != nil {
		t.Fatalf("PublishDecision should not return error when NATS is down, got: %v", err)
	}

	// Verify nothing was published (NATS unavailable)
	conn.mu.Lock()
	pubCount := len(conn.published)
	conn.mu.Unlock()
	if pubCount != 0 {
		t.Errorf("Expected 0 published when NATS unavailable, got %d", pubCount)
	}
}

// TestDecisionEvent_JSONRoundTrip verifies DecisionEvent serializes and
// deserializes correctly as JSON.
func TestDecisionEvent_JSONRoundTrip(t *testing.T) {
	event := sampleDecision()

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var decoded DecisionEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if decoded.DecisionID != event.DecisionID {
		t.Errorf("DecisionID = %q, want %q", decoded.DecisionID, event.DecisionID)
	}
	if decoded.WorkspaceID != event.WorkspaceID {
		t.Errorf("WorkspaceID = %q, want %q", decoded.WorkspaceID, event.WorkspaceID)
	}
	if decoded.ContentHash != event.ContentHash {
		t.Errorf("ContentHash = %q, want %q", decoded.ContentHash, event.ContentHash)
	}
	if decoded.Decision != event.Decision {
		t.Errorf("Decision = %q, want %q", decoded.Decision, event.Decision)
	}
	if decoded.Category != event.Category {
		t.Errorf("Category = %q, want %q", decoded.Category, event.Category)
	}
	if decoded.Confidence != event.Confidence {
		t.Errorf("Confidence = %f, want %f", decoded.Confidence, event.Confidence)
	}
	if !decoded.Timestamp.Equal(event.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", decoded.Timestamp, event.Timestamp)
	}
}

// TestNATSPublisher_SatisfiesDecisionPublisher verifies compile-time interface.
func TestNATSPublisher_SatisfiesDecisionPublisher(t *testing.T) {
	var _ DecisionPublisher = (*NATSPublisher)(nil)
}

// TestDecisionEvent_AllFieldsNonEmpty verifies the struct has all required fields.
func TestDecisionEvent_AllFieldsNonEmpty(t *testing.T) {
	event := sampleDecision()

	if event.DecisionID == "" {
		t.Error("decision_id must not be empty")
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
	if event.Category == "" {
		t.Error("category must not be empty")
	}
	if event.Confidence <= 0 {
		t.Error("confidence must be > 0")
	}
	if event.Timestamp.IsZero() {
		t.Error("timestamp must not be zero")
	}
}
