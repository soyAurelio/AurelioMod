// Package quarantine implements "Cuarentena Invertida" (Inverted Quarantine):
// content is blocked first, then analyzed. If the analysis returns CLEAN,
// the content is released. If BLOCKED, the content stays quarantined.
//
// State machine:
//
//	PENDING → ANALYZING → BLOCKED | RELEASED
//
// Content starts in PENDING (blocked immediately upon arrival). The Engine
// pipeline runs, and when WaveSpeed returns a decision:
//   - BLOCKED → content stays quarantined
//   - ALLOWED → content is released
//
// Quarantine state is stored in DragonflyDB with a 24-hour TTL. If the
// TTL expires before analysis completes, the content is auto-released
// as a safety measure (fail-open for false positives).
package quarantine

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// QuarantineState represents the current state in the inverted quarantine
// state machine.
type QuarantineState int

const (
	// StatePending means content has been quarantined and is awaiting analysis.
	// Content is BLOCKED while in this state ("cuarentena invertida").
	StatePending QuarantineState = iota

	// StateAnalyzing means the pipeline is currently processing the content.
	StateAnalyzing

	// StateBlocked means analysis confirmed the content is violating policy.
	// Content remains quarantined.
	StateBlocked

	// StateReleased means analysis found the content to be safe (CLEAN).
	// Content is released from quarantine.
	StateReleased
)

// String returns a human-readable representation of the quarantine state.
func (s QuarantineState) String() string {
	switch s {
	case StatePending:
		return "PENDING"
	case StateAnalyzing:
		return "ANALYZING"
	case StateBlocked:
		return "BLOCKED"
	case StateReleased:
		return "RELEASED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", s)
	}
}

// DefaultTTL is the default time-to-live for quarantine entries in DragonflyDB.
// After 24 hours, unanalyzed content is auto-released (fail-open safety).
const DefaultTTL = 24 * time.Hour

// QuarantineStatus holds the current state of a quarantined content item.
type QuarantineStatus struct {
	// ContentID is the unique content identifier from the request.
	ContentID string `json:"content_id"`

	// WorkspaceID is the workspace that owns the content.
	WorkspaceID string `json:"workspace_id"`

	// ContentHash is the BLAKE3 hash of the normalized content.
	ContentHash string `json:"content_hash"`

	// State is the current quarantine state (PENDING → ANALYZING → BLOCKED | RELEASED).
	State QuarantineState `json:"state"`

	// Decision is the moderation outcome ("blocked", "allowed").
	Decision string `json:"decision"`

	// Reason is the classification category or "safe".
	Reason string `json:"reason"`

	// Confidence is the AI model confidence score.
	Confidence float64 `json:"confidence"`

	// CreatedAt is when the content was first quarantined.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the last time the status was updated.
	UpdatedAt time.Time `json:"updated_at"`
}

// Content represents content being submitted for quarantine.
type Content struct {
	ContentID   string
	WorkspaceID string
	ContentHash string
}

// QuarantineStore defines the persistence layer for quarantine state.
// DragonflyDB serves as the backing store with TTL-based auto-expiry.
type QuarantineStore interface {
	// GetQuarantineState retrieves the current quarantine status for content.
	// Returns nil, nil if no entry exists.
	GetQuarantineState(ctx context.Context, contentID string) (*QuarantineStatus, error)

	// SetQuarantineState stores or updates the quarantine status with TTL.
	SetQuarantineState(ctx context.Context, contentID string, status *QuarantineStatus, ttl time.Duration) error
}

// QuarantineInterface defines the quarantine operations.
type QuarantineInterface interface {
	// Quarantine blocks content immediately and stores the quarantine state.
	// Returns the current QuarantineStatus (PENDING on first call, existing
	// state if already quarantined). This is idempotent.
	Quarantine(ctx context.Context, content *Content) (*QuarantineStatus, error)

	// UpdateStatus updates the quarantine state after analysis completes.
	// If decision is "allowed", transitions to RELEASED.
	// If decision is "blocked", transitions to BLOCKED.
	UpdateStatus(ctx context.Context, contentID string, decision string, reason string, confidence float64) (*QuarantineStatus, error)
}

// ErrNotFound is returned when attempting to update a non-existent quarantine entry.
var ErrNotFound = errors.New("quarantine: content not found")

// QuarantineManager implements QuarantineInterface with DragonflyDB-backed
// persistence. It enforces the "cuarentena invertida" state machine.
type QuarantineManager struct {
	store QuarantineStore
	ttl   time.Duration
}

// Compile-time interface check.
var _ QuarantineInterface = (*QuarantineManager)(nil)

// NewQuarantineManager creates a QuarantineManager backed by the given store
// with the specified TTL for quarantine entries.
func NewQuarantineManager(store QuarantineStore, ttl time.Duration) *QuarantineManager {
	return &QuarantineManager{
		store: store,
		ttl:   ttl,
	}
}

// Quarantine blocks content immediately and stores the quarantine state.
// On first call for a content ID, the state is PENDING.
// Subsequent calls return the existing state (idempotent).
func (m *QuarantineManager) Quarantine(ctx context.Context, content *Content) (*QuarantineStatus, error) {
	// Check if already quarantined
	existing, err := m.store.GetQuarantineState(ctx, content.ContentID)
	if err != nil {
		return nil, fmt.Errorf("quarantine get: %w", err)
	}
	if existing != nil {
		// Already quarantined — return existing state
		return existing, nil
	}

	// New quarantine entry: block immediately (PENDING state)
	now := time.Now()
	status := &QuarantineStatus{
		ContentID:   content.ContentID,
		WorkspaceID: content.WorkspaceID,
		ContentHash: content.ContentHash,
		State:       StatePending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := m.store.SetQuarantineState(ctx, content.ContentID, status, m.ttl); err != nil {
		return nil, fmt.Errorf("quarantine set: %w", err)
	}

	return status, nil
}

// UpdateStatus updates the quarantine state after analysis completes.
// Transitions from PENDING to RELEASED (clean) or BLOCKED (violation).
// Returns ErrNotFound if the content is not in quarantine.
func (m *QuarantineManager) UpdateStatus(ctx context.Context, contentID string, decision string, reason string, confidence float64) (*QuarantineStatus, error) {
	existing, err := m.store.GetQuarantineState(ctx, contentID)
	if err != nil {
		return nil, fmt.Errorf("quarantine get: %w", err)
	}
	if existing == nil {
		return nil, ErrNotFound
	}

	// If already in a terminal state, return current state (idempotent)
	if existing.State == StateReleased || existing.State == StateBlocked {
		return existing, nil
	}

	// Transition based on decision
	var newState QuarantineState
	if decision == "allowed" || decision == "DECISION_ALLOW" {
		newState = StateReleased
	} else {
		newState = StateBlocked
	}

	now := time.Now()
	status := &QuarantineStatus{
		ContentID:   existing.ContentID,
		WorkspaceID: existing.WorkspaceID,
		ContentHash: existing.ContentHash,
		State:       newState,
		Decision:    decision,
		Reason:      reason,
		Confidence:  confidence,
		CreatedAt:   existing.CreatedAt,
		UpdatedAt:   now,
	}

	if err := m.store.SetQuarantineState(ctx, contentID, status, m.ttl); err != nil {
		return nil, fmt.Errorf("quarantine set: %w", err)
	}

	return status, nil
}
