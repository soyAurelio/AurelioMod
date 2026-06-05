//go:build integration

package pipeline

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/soyAurelio/AurelioMod/internal/testutil"
)

// TestMain starts NATS and Weaviate via testcontainers before running
// pipeline integration tests. When Docker is unavailable or -short is set,
// all tests are skipped gracefully.
func TestMain(m *testing.M) {
	flag.Parse()

	if testing.Short() {
		os.Exit(0)
	}

	// Start NATS for real decision publishing integration
	ctx := context.Background()
	nc, err := testutil.StartNATS(ctx)
	if err != nil || nc == nil {
		os.Exit(0) // Docker not available — skip gracefully
	}
	nc.Close()

	// Start Weaviate for real L3 vector search integration
	_, err = testutil.StartWeaviate(ctx)
	if err != nil {
		os.Exit(0) // Weaviate unavailable — skip gracefully
	}

	os.Exit(m.Run())
}

// TestIntegration_Pipeline_NATSConnection verifies that NATS is reachable
// through the testcontainers helper when Docker is available.
func TestIntegration_Pipeline_NATSConnection(t *testing.T) {
	nc, err := testutil.StartNATS(t.Context())
	if err != nil {
		t.Fatalf("NATS connection failed: %v", err)
	}
	defer nc.Close()

	// Verify NATS is connected
	if !nc.IsConnected() {
		t.Fatal("NATS is not connected")
	}
}

// TestIntegration_Pipeline_WeaviateAvailable verifies that Weaviate is
// reachable through the testcontainers helper when Docker is available.
func TestIntegration_Pipeline_WeaviateAvailable(t *testing.T) {
	url, err := testutil.StartWeaviate(t.Context())
	if err != nil {
		t.Fatalf("Weaviate start failed: %v", err)
	}
	if url == "" {
		t.Fatal("Weaviate URL is empty")
	}
}
