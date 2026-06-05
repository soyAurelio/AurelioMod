package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/soyAurelio/AurelioMod/internal/paseto"
)

// newTestTokenManager creates a fresh PASETO TokenManager for testing
// and returns an admin token signed with it.
func newTestTokenManager(t *testing.T) (*paseto.TokenManager, string) {
	t.Helper()
	tm, err := paseto.New()
	if err != nil {
		t.Fatalf("paseto.New: %v", err)
	}
	token, err := tm.ServiceToken("admin", 5*time.Minute)
	if err != nil {
		t.Fatalf("ServiceToken: %v", err)
	}
	return tm, token
}

// TestPprofAuth_ValidToken verifies that a request with a valid PASETO
// admin token receives HTTP 200 from the pprof endpoint.
func TestPprofAuth_ValidToken(t *testing.T) {
	tm, token := newTestTokenManager(t)
	handler := pprofMux(tm)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestPprofAuth_MissingToken verifies that a request without an
// Authorization header receives HTTP 401 with the correct error body.
func TestPprofAuth_MissingToken(t *testing.T) {
	tm, _ := newTestTokenManager(t)
	handler := pprofMux(tm)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	body := rec.Body.String()
	if body != `{"error":"missing admin token"}` {
		t.Errorf("body = %q, want %q", body, `{"error":"missing admin token"}`)
	}
}

// TestPprofAuth_ExpiredToken verifies that an expired PASETO token
// receives HTTP 401 with "invalid admin token" in the error body.
func TestPprofAuth_ExpiredToken(t *testing.T) {
	tm, err := paseto.New()
	if err != nil {
		t.Fatalf("paseto.New: %v", err)
	}
	// Create a token with a TTL of -1 second (already expired)
	expiredToken, err := tm.ServiceToken("admin", -1*time.Second)
	if err != nil {
		t.Fatalf("ServiceToken: %v", err)
	}

	handler := pprofMux(tm)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	body := rec.Body.String()
	if body != `{"error":"invalid admin token"}` {
		t.Errorf("body = %q, want %q", body, `{"error":"invalid admin token"}`)
	}
}

// TestPprofAuth_UnsignedToken verifies that a tampered/unsigned token
// receives HTTP 401.
func TestPprofAuth_UnsignedToken(t *testing.T) {
	// Create two different token managers — the token from tm1 won't
	// verify with tm2's public key.
	tm := mustTokenManager(t)

	otherTm, err := paseto.New()
	if err != nil {
		t.Fatalf("paseto.New: %v", err)
	}
	otherToken, err := otherTm.ServiceToken("admin", 5*time.Minute)
	if err != nil {
		t.Fatalf("ServiceToken: %v", err)
	}

	handler := pprofMux(tm)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d; body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// mustTokenManager is a test helper that creates a TokenManager or fails.
func mustTokenManager(t *testing.T) *paseto.TokenManager {
	t.Helper()
	tm, err := paseto.New()
	if err != nil {
		t.Fatalf("paseto.New: %v", err)
	}
	return tm
}
