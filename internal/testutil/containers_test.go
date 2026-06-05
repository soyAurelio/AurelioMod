package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestContainers_StartDragonfly verifies that StartDragonfly returns a connected
// go-redis client. The container starts once (sync.Once) and is reused across tests.
func TestContainers_StartDragonfly(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test: requires Docker")
	}
	if !dockerAvailable() {
		t.Skip("Skipping: Docker not available")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	rdb, err := StartDragonfly(ctx)
	if err != nil {
		t.Fatalf("StartDragonfly: %v", err)
	}
	if rdb == nil {
		t.Fatal("StartDragonfly returned nil client")
	}

	// Verify connection works
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("DragonflyDB ping failed: %v", err)
	}

	// Verify we can write and read (real DragonflyDB, not mock)
	key := "testutil:self-test:dragonfly"
	rdb.Set(ctx, key, "ok", 5*time.Second)
	val, err := rdb.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("DragonflyDB read/write: %v", err)
	}
	if val != "ok" {
		t.Fatalf("DragonflyDB value = %q, want %q", val, "ok")
	}
	rdb.Del(ctx, key)
}

// TestContainers_StartNATS verifies that StartNATS returns a connected NATS client.
func TestContainers_StartNATS(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test: requires Docker")
	}
	if !dockerAvailable() {
		t.Skip("Skipping: Docker not available")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	nc, err := StartNATS(ctx)
	if err != nil {
		t.Fatalf("StartNATS: %v", err)
	}
	if nc == nil {
		t.Fatal("StartNATS returned nil client")
	}
	defer nc.Close()

	// Verify connection works
	if status := nc.Status(); status != nats.CONNECTED {
		t.Fatalf("NATS status = %v, want CONNECTED", status)
	}
}

// TestContainers_StartWeaviate verifies that StartWeaviate returns a URL
// pointing to a running Weaviate instance.
func TestContainers_StartWeaviate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test: requires Docker")
	}
	if !dockerAvailable() {
		t.Skip("Skipping: Docker not available")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	url, err := StartWeaviate(ctx)
	if err != nil {
		t.Fatalf("StartWeaviate: %v", err)
	}
	if url == "" {
		t.Fatal("StartWeaviate returned empty URL")
	}

	// Verify the URL is plausible
	if !stringsHasPrefix(url, "http://") {
		t.Errorf("Weaviate URL = %q, want http:// prefix", url)
	}
}

// TestContainers_NoDockerReturnsError verifies that when Docker is not available,
// StartDragonfly returns an error (graceful degradation).
func TestContainers_NoDockerReturnsError(t *testing.T) {
	if !dockerAvailable() {
		// Docker IS not available — StartDragonfly should return error
		ctx := t.Context()
		_, err := StartDragonfly(ctx)
		if err == nil {
			t.Error("expected error when Docker is not available, got nil")
		}
	} else {
		t.Skip("Docker is available — skipping error-path test")
	}
}

// TestStringsHasPrefix verifies the string prefix helper used in URL validation.
func TestStringsHasPrefix(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		prefix string
		want   bool
	}{
		{"exact match", "http://localhost:8080", "http://", true},
		{"empty string", "", "http://", false},
		{"shorter than prefix", "ab", "http://", false},
		{"different prefix", "https://host", "http://", false},
		{"partial match", "ht", "http", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringsHasPrefix(tt.s, tt.prefix)
			if got != tt.want {
				t.Errorf("stringsHasPrefix(%q, %q) = %v, want %v", tt.s, tt.prefix, got, tt.want)
			}
		})
	}
}

// TestDockerAvailable_DoesNotPanic verifies the dockerAvailable check
// doesn't panic and returns a boolean.
func TestDockerAvailable_DoesNotPanic(t *testing.T) {
	_ = dockerAvailable() // just verify it doesn't panic
}

// TestContainers_SyncOnceReuse verifies that calling StartDragonfly twice
// returns the same client (sync.Once ensures single start).
func TestContainers_SyncOnceReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test: requires Docker")
	}
	if !dockerAvailable() {
		t.Skip("Skipping: Docker not available")
	}

	ctx := t.Context()

	rdb1, err := StartDragonfly(ctx)
	if err != nil {
		t.Fatalf("StartDragonfly call 1: %v", err)
	}

	rdb2, err := StartDragonfly(ctx)
	if err != nil {
		t.Fatalf("StartDragonfly call 2: %v", err)
	}

	// Both calls should return the same pointer
	if rdb1 != rdb2 {
		t.Error("sync.Once should return the same client on repeated calls")
	}
}
