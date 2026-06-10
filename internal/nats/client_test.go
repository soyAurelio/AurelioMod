package nats

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.URL != "nats://localhost:4222" {
		t.Errorf("URL = %s, want nats://localhost:4222", cfg.URL)
	}
}

func TestConnectTimeout(t *testing.T) {
	// Connect to a non-routable IP to verify timeout works
	cfg := Config{URL: "nats://10.255.255.1:4222"}
	start := time.Now()
	_, err := Connect(cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected connection error to unroutable host, got nil")
	}
	if elapsed > 10*time.Second {
		t.Errorf("timeout took too long: %v (expected ~5s)", elapsed)
	}
}

func TestConnectInvalidURL(t *testing.T) {
	cfg := Config{URL: "invalid://bad-url"}
	_, err := Connect(cfg)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
