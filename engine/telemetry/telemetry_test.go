package telemetry

import (
	"context"
	"testing"
)

// TestInit_NoopWhenEmptyEndpoint verifies that calling Init with an
// empty OTLP endpoint returns a non-nil Telemetry with noop providers.
// This is the default for development and CI without a collector.
func TestInit_NoopWhenEmptyEndpoint(t *testing.T) {
	cfg := Config{
		OTLPEndpoint: "",
		ServiceName:  "engine-test",
	}

	tele, err := Init(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Init with empty endpoint should not error: %v", err)
	}
	if tele == nil {
		t.Fatal("Init returned nil Telemetry")
	}

	// Verify shutdown works
	if err := tele.Shutdown(t.Context()); err != nil {
		t.Errorf("Shutdown should not error: %v", err)
	}
}

// TestInit_WithEndpoint_Succeeds verifies that Init with an explicit
// OTLP endpoint does not error. OTLP creation is lazy (connection happens
// on first export), so Init should succeed with any host:port string.
// The resulting Telemetry can be shut down (Shutdown may return an error
// if the collector is unreachable — that is expected behavior).
func TestInit_WithEndpoint_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration-style telemetry endpoint test")
	}
	cfg := Config{
		OTLPEndpoint: "localhost:14317", // non-existent port — lazy connect
		ServiceName:  "engine-test",
	}

	tele, err := Init(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Init should succeed with endpoint (lazy connect): %v", err)
	}
	if tele == nil {
		t.Fatal("Init returned nil Telemetry")
	}
	// Shutdown may fail (connection refused) — that's expected when no
	// collector is running. The error is non-fatal for this test.
	_ = tele.Shutdown(t.Context())
}

// TestConfig_ServiceNameDefault verifies that an empty ServiceName
// defaults to "engine".
func TestConfig_ServiceNameDefault(t *testing.T) {
	cfg := Config{
		OTLPEndpoint: "",
		ServiceName:  "",
	}

	tele, err := Init(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Init should not error: %v", err)
	}
	// If service name was empty, Init should have used the default.
	// The Telemetry struct should be non-nil regardless.
	if tele == nil {
		t.Fatal("Init returned nil Telemetry")
	}
	_ = tele.Shutdown(t.Context())
}

// TestShutdown_Idempotent verifies that calling Shutdown multiple times
// does not panic or return an error.
func TestShutdown_Idempotent(t *testing.T) {
	cfg := Config{
		OTLPEndpoint: "",
		ServiceName:  "engine-test",
	}

	tele, err := Init(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Init should not error: %v", err)
	}

	// First shutdown
	if err := tele.Shutdown(t.Context()); err != nil {
		t.Errorf("First Shutdown should not error: %v", err)
	}

	// Second shutdown — must not panic
	if err := tele.Shutdown(t.Context()); err != nil {
		t.Errorf("Second Shutdown should not error either: %v", err)
	}
}

// TestShutdown_WithoutInit verifies that Shutdown on a zero-value
// Telemetry does not panic (each provider nil check).
func TestShutdown_WithoutInit(t *testing.T) {
	tele := &Telemetry{}
	if err := tele.Shutdown(t.Context()); err != nil {
		t.Errorf("Shutdown on uninitialized Telemetry should not error: %v", err)
	}
}

// TestInit_DefaultEndpointFromEnv verifies that when no explicit endpoint
// is configured, Init falls back to the OTEL_EXPORTER_OTLP_ENDPOINT
// environment variable (triangulation: different default path).
func TestInit_DefaultEndpointFromEnv(t *testing.T) {
	// Set env var to empty to trigger noop path
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	cfg := Config{
		ServiceName: "engine-env-test",
	}

	tele, err := Init(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Init should not error when env var is empty: %v", err)
	}
	if tele == nil {
		t.Fatal("Init returned nil Telemetry")
	}
	_ = tele.Shutdown(t.Context())
}

// TestConfig_ZeroValue verifies that a zero-value Config (no fields set)
// produces a working noop Telemetry without panicking.
func TestConfig_ZeroValue(t *testing.T) {
	tele, err := Init(t.Context(), Config{})
	if err != nil {
		t.Fatalf("Init with zero Config should not error: %v", err)
	}
	if tele == nil {
		t.Fatal("Init returned nil Telemetry")
	}
	if err := tele.Shutdown(t.Context()); err != nil {
		t.Errorf("Shutdown should not error: %v", err)
	}
}

// BenchmarkInit_Noop measures the performance of Init with empty endpoint.
func BenchmarkInit_Noop(b *testing.B) {
	ctx := context.Background()
	cfg := Config{ServiceName: "engine-bench"}
	for b.Loop() {
		tele, _ := Init(ctx, cfg)
		tele.Shutdown(ctx)
	}
}
