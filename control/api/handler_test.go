package controlapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// assertAnError is a sentinel used in mock TokenManager.VerifyToken failures.
var assertAnError = errors.New("mock: token invalid")

// --- TokenManager mock ---

type mockTokenManager struct {
	verifyFunc func(signed string) (Token, error)
	serviceFunc func(name string, ttl time.Duration) (string, error)
}

type mockToken struct{ sub string }

func (m *mockToken) Subject() string { return m.sub }

func (tm *mockTokenManager) VerifyToken(signed string) (Token, error) {
	if tm.verifyFunc != nil {
		return tm.verifyFunc(signed)
	}
	return &mockToken{sub: "test-workspace-id"}, nil
}

func (tm *mockTokenManager) ServiceToken(name string, ttl time.Duration) (string, error) {
	if tm.serviceFunc != nil {
		return tm.serviceFunc(name, ttl)
	}
	return "mock-token-" + name, nil
}

// --- DB helpers ---

// testDB creates an in-memory SQLite-style DB for testing.
// We use a real Postgres-compatible approach via testcontainers in integration,
// but for unit tests we inject the DB handle and rely on the caller to set up tables.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	// For unit tests, we use a nil-safe approach: handlers that fail gracefully
	// when db is nil (we test the HTTP layer separately from the DB layer).
	return nil
}

func newTestApp(db *sql.DB, tm TokenManager) *fiber.App {
	return New(db, tm)
}

func doRequest(t *testing.T, app *fiber.App, method, path, token string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, path, body)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func mustJSONBody(s string) io.Reader {
	return bytes.NewReader([]byte(s))
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("json.Decode: %v", err)
	}
	return result
}

// --- Health check ---

func TestHealthz(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "GET", "/healthz", "", nil)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// --- Auth middleware ---

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "GET", "/v1/workspaces", "", nil)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	tm := &mockTokenManager{
		verifyFunc: func(signed string) (Token, error) {
			return nil, assertAnError
		},
	}
	app := New(nil, tm)
	resp := doRequest(t, app, "GET", "/v1/workspaces", "bad-token", nil)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	tm := &mockTokenManager{
		verifyFunc: func(signed string) (Token, error) {
			if signed != "valid-token" {
				return nil, assertAnError
			}
			return &mockToken{sub: "ws-123"}, nil
		},
	}
	app := New(nil, tm)
	// This will fail at DB level (nil db), but should pass auth middleware
	resp := doRequest(t, app, "GET", "/v1/workspaces", "valid-token", nil)

	// nil DB → 500, not 401 (auth passed, but DB failed)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("auth middleware rejected valid token (status 401)")
	}
}

// --- Auth login endpoint ---

func TestAuthLogin_MissingAPIKey(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "POST", "/v1/auth/login", "", mustJSONBody(`{}`))

	body := readJSON(t, resp)
	if body["error"] != "api_key is required" {
		t.Errorf("error = %v, want 'api_key is required'", body["error"])
	}
}

func TestAuthLogin_InvalidJSON(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	req, _ := http.NewRequest("POST", "/v1/auth/login", mustJSONBody(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// --- Routes exist ---

func TestRoutes_Exist(t *testing.T) {
	app := New(nil, &mockTokenManager{})

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/healthz"},
		{"POST", "/v1/auth/login"},
		{"POST", "/v1/auth/refresh"},
		{"GET", "/v1/workspaces"},
		{"POST", "/v1/workspaces"},
		{"GET", "/v1/workspaces/123"},
		{"GET", "/v1/workspaces/123/stats"},
		{"GET", "/v1/workspaces/123/decisions"},
		{"GET", "/v1/workspaces/123/decisions/evt_abc"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			token := ""
			if tt.path != "/healthz" && tt.path != "/v1/auth/login" {
				token = "valid-token"
			}
			resp := doRequest(t, app, tt.method, tt.path, token, nil)
			// 405 = route exists but wrong method, 401 = auth required, 200/500 = handled
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("route %s %s returned 404 — route not registered", tt.method, tt.path)
			}
		})
	}
}

func TestConsumeRoute_Exists(t *testing.T) {
	app := New(nil, &mockTokenManager{})
	resp := doRequest(t, app, "POST", "/v1/workspaces/123/consume", "valid-token", nil)
	// nil DB → 500, but route must exist (not 404)
	if resp.StatusCode == http.StatusNotFound {
		t.Error("consume route not registered (got 404)")
	}
}
