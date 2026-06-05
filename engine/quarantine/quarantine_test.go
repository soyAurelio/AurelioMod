package quarantine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- mock implementations ---

// mockQuarantineStore implements QuarantineStore for testing.
type mockQuarantineStore struct {
	mu     sync.Mutex
	states map[string]*QuarantineStatus
}

func newMockStore() *mockQuarantineStore {
	return &mockQuarantineStore{
		states: make(map[string]*QuarantineStatus),
	}
}

func (m *mockQuarantineStore) GetQuarantineState(ctx context.Context, contentID string) (*QuarantineStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[contentID]
	if !ok {
		return nil, nil
	}
	// Return a copy
	copy := *s
	return &copy, nil
}

func (m *mockQuarantineStore) SetQuarantineState(ctx context.Context, contentID string, status *QuarantineStatus, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := *status
	m.states[contentID] = &copy
	return nil
}

// mockFailingStore is a QuarantineStore that returns errors on Set.
type mockFailingStore struct{}

func (m *mockFailingStore) GetQuarantineState(ctx context.Context, contentID string) (*QuarantineStatus, error) {
	return nil, nil
}

func (m *mockFailingStore) SetQuarantineState(ctx context.Context, contentID string, status *QuarantineStatus, ttl time.Duration) error {
	return errors.New("DragonflyDB connection refused")
}

// --- helpers ---

func sampleContent() *Content {
	return &Content{
		ContentID:   "cnt_abc123",
		WorkspaceID: "ws_xyz",
		ContentHash: "b3:a1b2c3d4",
	}
}

// --- tests ---

// TestQuarantineManager_Quarantine_InitialState verifies that Quarantine
// transitions content from nothing → PENDING (blocked immediately as per
// "cuarentena invertida" principle).
func TestQuarantineManager_Quarantine_InitialState(t *testing.T) {
	store := newMockStore()
	mgr := NewQuarantineManager(store, DefaultTTL)

	content := sampleContent()
	status, err := mgr.Quarantine(t.Context(), content)
	if err != nil {
		t.Fatalf("Quarantine error: %v", err)
	}

	if status.State != StatePending {
		t.Errorf("State = %v, want %v", status.State, StatePending)
	}
	if status.ContentID != "cnt_abc123" {
		t.Errorf("ContentID = %q, want \"cnt_abc123\"", status.ContentID)
	}
	if status.WorkspaceID != "ws_xyz" {
		t.Errorf("WorkspaceID = %q, want \"ws_xyz\"", status.WorkspaceID)
	}
	if status.CreatedAt.IsZero() {
		t.Error("CreatedAt must not be zero")
	}
	if status.UpdatedAt.IsZero() {
		t.Error("UpdatedAt must not be zero")
	}

	// Verify stored in DragonflyDB
	stored, err := store.GetQuarantineState(t.Context(), "cnt_abc123")
	if err != nil {
		t.Fatalf("GetQuarantineState error: %v", err)
	}
	if stored == nil {
		t.Fatal("Expected stored state, got nil")
	}
	if stored.State != StatePending {
		t.Errorf("Stored State = %v, want %v", stored.State, StatePending)
	}
}

// TestQuarantineManager_UpdateStatus_Release verifies that UpdateStatus
// transitions PENDING → RELEASED when analysis returns clean.
func TestQuarantineManager_UpdateStatus_Release(t *testing.T) {
	store := newMockStore()
	mgr := NewQuarantineManager(store, DefaultTTL)

	content := sampleContent()

	// First quarantine (PENDING)
	_, err := mgr.Quarantine(t.Context(), content)
	if err != nil {
		t.Fatalf("Quarantine error: %v", err)
	}

	// Then release (CLEAN)
	status, err := mgr.UpdateStatus(t.Context(), "cnt_abc123", "allowed", "safe", 0.99)
	if err != nil {
		t.Fatalf("UpdateStatus error: %v", err)
	}

	if status.State != StateReleased {
		t.Errorf("State = %v, want %v", status.State, StateReleased)
	}
	if status.Reason != "safe" {
		t.Errorf("Reason = %q, want \"safe\"", status.Reason)
	}
	if status.Decision != "allowed" {
		t.Errorf("Decision = %q, want \"allowed\"", status.Decision)
	}
	if status.Confidence != 0.99 {
		t.Errorf("Confidence = %f, want 0.99", status.Confidence)
	}

	// Releasing again should be idempotent (already released)
	status2, err := mgr.UpdateStatus(t.Context(), "cnt_abc123", "allowed", "safe", 0.99)
	if err != nil {
		t.Fatalf("UpdateStatus (idempotent) error: %v", err)
	}
	if status2.State != StateReleased {
		t.Errorf("State (idempotent) = %v, want %v", status2.State, StateReleased)
	}
}

// TestQuarantineManager_UpdateStatus_Blocked verifies that UpdateStatus
// transitions PENDING → BLOCKED when analysis confirms violation.
func TestQuarantineManager_UpdateStatus_Blocked(t *testing.T) {
	store := newMockStore()
	mgr := NewQuarantineManager(store, DefaultTTL)

	content := sampleContent()

	// First quarantine (PENDING)
	_, err := mgr.Quarantine(t.Context(), content)
	if err != nil {
		t.Fatalf("Quarantine error: %v", err)
	}

	// Then block
	status, err := mgr.UpdateStatus(t.Context(), "cnt_abc123", "blocked", "violence_graphic", 0.95)
	if err != nil {
		t.Fatalf("UpdateStatus error: %v", err)
	}

	if status.State != StateBlocked {
		t.Errorf("State = %v, want %v", status.State, StateBlocked)
	}
	if status.Reason != "violence_graphic" {
		t.Errorf("Reason = %q, want \"violence_graphic\"", status.Reason)
	}
	if status.Decision != "blocked" {
		t.Errorf("Decision = %q, want \"blocked\"", status.Decision)
	}
}

// TestQuarantineManager_UpdateStatus_NotFound verifies that updating a
// non-existent content ID returns an error.
func TestQuarantineManager_UpdateStatus_NotFound(t *testing.T) {
	store := newMockStore()
	mgr := NewQuarantineManager(store, DefaultTTL)

	_, err := mgr.UpdateStatus(t.Context(), "nonexistent", "blocked", "violence", 0.9)
	if err == nil {
		t.Fatal("Expected error for non-existent content, got nil")
	}
}

// TestQuarantineManager_Quarantine_Idempotent verifies that calling
// Quarantine twice on the same content is idempotent (returns existing state).
func TestQuarantineManager_Quarantine_Idempotent(t *testing.T) {
	store := newMockStore()
	mgr := NewQuarantineManager(store, DefaultTTL)

	content := sampleContent()

	status1, err := mgr.Quarantine(t.Context(), content)
	if err != nil {
		t.Fatalf("First Quarantine error: %v", err)
	}

	status2, err := mgr.Quarantine(t.Context(), content)
	if err != nil {
		t.Fatalf("Second Quarantine error: %v", err)
	}

	if status2.State != status1.State {
		t.Errorf("Idempotent State = %v, want %v", status2.State, status1.State)
	}
	if status2.ContentID != status1.ContentID {
		t.Errorf("Idempotent ContentID = %q, want %q", status2.ContentID, status1.ContentID)
	}
}

// TestQuarantineManager_StoreFailure verifies that when DragonflyDB is
// unavailable, Quarantine returns an error.
func TestQuarantineManager_StoreFailure(t *testing.T) {
	store := &mockFailingStore{}
	mgr := NewQuarantineManager(store, DefaultTTL)

	content := sampleContent()
	_, err := mgr.Quarantine(t.Context(), content)
	if err == nil {
		t.Fatal("Expected error when store is unavailable, got nil")
	}
}

// TestQuarantineStatus_StateValues verifies that all states have distinct
// integer values.
func TestQuarantineStatus_StateValues(t *testing.T) {
	states := map[QuarantineState]bool{}
	for _, s := range []QuarantineState{StatePending, StateAnalyzing, StateBlocked, StateReleased} {
		if states[s] {
			t.Errorf("Duplicate state value: %d", s)
		}
		states[s] = true
	}

	if len(states) != 4 {
		t.Errorf("Expected 4 distinct states, got %d", len(states))
	}
}

// TestQuarantineStatus_StringRepresentation verifies String() returns
// human-readable names.
func TestQuarantineStatus_StringRepresentation(t *testing.T) {
	tests := []struct {
		state QuarantineState
		want  string
	}{
		{StatePending, "PENDING"},
		{StateAnalyzing, "ANALYZING"},
		{StateBlocked, "BLOCKED"},
		{StateReleased, "RELEASED"},
	}

	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// TestQuarantine_SatisfiesInterface verifies that QuarantineManager
// implements the QuarantineInterface.
func TestQuarantine_SatisfiesInterface(t *testing.T) {
	var _ QuarantineInterface = (*QuarantineManager)(nil)
}

// TestQuarantineManager_DefaultTTL verifies the default TTL is 24 hours.
func TestQuarantineManager_DefaultTTL(t *testing.T) {
	if DefaultTTL != 24*time.Hour {
		t.Errorf("DefaultTTL = %v, want 24h", DefaultTTL)
	}
}
