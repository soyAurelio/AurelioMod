package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlanClient_Consume_NotConfigured(t *testing.T) {
	// Fail-closed: when baseURL or token is empty, consume must return false.
	client := NewPlanClient("", "")
	if client.Consume(t.Context(), "ws-001") {
		t.Error("Consume should deny when client is not configured (fail-closed)")
	}
}

func TestPlanClient_Consume_NoToken(t *testing.T) {
	client := NewPlanClient("http://localhost:8080", "")
	if client.Consume(t.Context(), "ws-001") {
		t.Error("Consume should deny when token is empty (fail-closed)")
	}
}

func TestPlanClient_Consume_NoURL(t *testing.T) {
	client := NewPlanClient("", "token123")
	if client.Consume(t.Context(), "ws-001") {
		t.Error("Consume should deny when baseURL is empty (fail-closed)")
	}
}

func TestPlanClient_Consume_Allowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Verify auth header
		if ah := r.Header.Get("Authorization"); ah != "Bearer test-token" {
			t.Errorf("Authorization = %q, want 'Bearer test-token'", ah)
		}

		// Verify content type
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want 'application/json'", ct)
		}

		// Verify URL path
		if !strings.Contains(r.URL.Path, "ws-001/consume") {
			t.Errorf("URL path = %q, want /v1/workspaces/ws-001/consume", r.URL.Path)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ConsumeResponse{Allowed: true})
	}))
	defer server.Close()

	client := NewPlanClient(server.URL, "test-token")
	if !client.Consume(t.Context(), "ws-001") {
		t.Error("Consume should allow when server returns Allowed: true")
	}
}

func TestPlanClient_Consume_Exhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewPlanClient(server.URL, "test-token")
	if client.Consume(t.Context(), "ws-001") {
		t.Error("Consume should deny when quota exhausted (429)")
	}
}

func TestPlanClient_Consume_ServerError_FailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewPlanClient(server.URL, "test-token")
	// Server error: fail-closed (safer than allowing unbilled consumption)
	if client.Consume(t.Context(), "ws-001") {
		t.Error("Consume should deny on server error (fail-closed)")
	}
}

func TestPlanClient_Consume_Unreachable_FailClosed(t *testing.T) {
	// Point to a closed port — connection refused.
	client := NewPlanClient("http://127.0.0.1:19999", "test-token")
	if client.Consume(t.Context(), "ws-001") {
		t.Error("Consume should deny when server is unreachable (fail-closed)")
	}
}

func TestPlanClient_Consume_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler exists but context is already canceled.
	}))
	defer server.Close()

	client := NewPlanClient(server.URL, "test-token")
	ctx, cancel := context.WithTimeout(context.Background(), 0) // immediately expired
	defer cancel()

	if client.Consume(ctx, "ws-001") {
		t.Error("Consume should deny when context is canceled")
	}
}
