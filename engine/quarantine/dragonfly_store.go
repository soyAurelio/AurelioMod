package quarantine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Compile-time interface check.
var _ QuarantineStore = (*DragonflyStore)(nil)

// DragonflyStore implements QuarantineStore backed by DragonflyDB (Redis-compatible).
// Uses the same Redis client as the L1/L2 cache for operational simplicity.
type DragonflyStore struct {
	rdb    *redis.Client
	prefix string
}

// NewDragonflyStore creates a quarantine store backed by DragonflyDB.
// The prefix allows namespace isolation (e.g., "quarantine:").
func NewDragonflyStore(rdb *redis.Client, prefix string) *DragonflyStore {
	if prefix == "" {
		prefix = "quarantine:"
	}
	return &DragonflyStore{
		rdb:    rdb,
		prefix: prefix,
	}
}

func (s *DragonflyStore) key(contentID string) string {
	return s.prefix + contentID
}

// GetQuarantineState retrieves the current quarantine status for content.
func (s *DragonflyStore) GetQuarantineState(ctx context.Context, contentID string) (*QuarantineStatus, error) {
	data, err := s.rdb.Get(ctx, s.key(contentID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("quarantine get: %w", err)
	}

	var status QuarantineStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("quarantine unmarshal: %w", err)
	}
	return &status, nil
}

// SetQuarantineState stores or updates the quarantine status with TTL.
func (s *DragonflyStore) SetQuarantineState(ctx context.Context, contentID string, status *QuarantineStatus, ttl time.Duration) error {
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("quarantine marshal: %w", err)
	}
	if err := s.rdb.Set(ctx, s.key(contentID), data, ttl).Err(); err != nil {
		return fmt.Errorf("quarantine set: %w", err)
	}
	return nil
}
