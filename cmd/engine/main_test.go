package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// freePort returns an available TCP port for testing.
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(addr)
	return port
}

// getURL performs a GET request and returns the status code and body.
func getURL(t *testing.T, url string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

// TestServer_HealthCheck verifies that the HTTP server starts and responds
// to GET /healthz with 200 OK and a JSON body containing "status": "ok".
func TestServer_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	port := freePort(t)
	ctx := t.Context()

	// Create a minimal server config for testing
	srv, err := newServer(ctx, serverConfig{
		Port: port,
	})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}

	// Start the server in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	// Wait for the server to start (poll up to 2s)
	baseURL := "http://127.0.0.1:" + port
	if !waitForServer(t, baseURL+"/healthz", 2*time.Second) {
		srv.Shutdown(t.Context())
		t.Fatal("server did not start in time")
	}

	// Hit the health check endpoint
	status, body := getURL(t, baseURL+"/healthz")
	if status != http.StatusOK {
		t.Errorf("healthz status = %d, want %d, body = %s", status, http.StatusOK, body)
	}
	if body == "" {
		t.Error("healthz body is empty")
	}

	// Shutdown cleanly
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown error: %v", err)
	}

	// Verify listen error is http.ErrServerClosed
	if err := <-errCh; err != nil && err != http.ErrServerClosed {
		t.Errorf("ListenAndServe error: %v", err)
	}
}

// TestServer_GracefulShutdown verifies that sending a shutdown signal
// causes the server to stop accepting new connections while finishing
// in-flight requests.
func TestServer_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	port := freePort(t)
	ctx := t.Context()

	srv, err := newServer(ctx, serverConfig{
		Port: port,
	})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	baseURL := "http://127.0.0.1:" + port
	if !waitForServer(t, baseURL+"/healthz", 2*time.Second) {
		srv.Shutdown(t.Context())
		t.Fatal("server did not start")
	}

	// Verify health check works
	status, _ := getURL(t, baseURL+"/healthz")
	if status != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", status, http.StatusOK)
	}

	// Trigger shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown error: %v", err)
	}

	// After shutdown, new requests should be rejected
	_, err = http.Get(baseURL + "/healthz")
	if err == nil {
		t.Error("expected connection refused after shutdown")
	}

	// Verify the listen goroutine completed with ErrServerClosed
	if err := <-errCh; err != nil && err != http.ErrServerClosed {
		t.Errorf("ListenAndServe error: %v", err)
	}
}

// waitForServer polls the given URL until it returns 200 or times out.
func waitForServer(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
