package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	pasetolib "aidanwoods.dev/go-paseto"
	v1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// requireUnauthenticated asserts that err is a connect.Error with code Unauthenticated.
// Used across multiple test functions to reduce duplication.
func requireUnauthenticated(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected Unauthenticated error, got nil")
	}
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", connectErr.Code())
	}
}

// echoNext is a UnaryFunc that passes through and records whether it was called.
type echoNext struct {
	called bool
	ctx    context.Context
}

func (e *echoNext) call(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
	e.called = true
	e.ctx = ctx
	return connect.NewResponse(&v1.AnalyzeResponse{Decision: v1.Decision_DECISION_BLOCK}), nil
}

// newTestKeypair creates a fresh Ed25519 keypair for test tokens.
func newTestKeypair() (pasetolib.V4AsymmetricSecretKey, pasetolib.V4AsymmetricPublicKey) {
	sk := pasetolib.NewV4AsymmetricSecretKey()
	return sk, sk.Public()
}

// signTestToken creates a valid PASETO v4 token with workspace_id claim.
func signTestToken(sk pasetolib.V4AsymmetricSecretKey, workspaceID string, ttl time.Duration) string {
	token := pasetolib.NewToken()
	token.SetIssuer("aureliomod")
	token.SetSubject("test-service")
	token.SetIssuedAt(time.Now())
	token.SetNotBefore(time.Now())
	token.SetExpiration(time.Now().Add(ttl))
	token.SetString("workspace_id", workspaceID)
	return token.V4Sign(sk, nil)
}

// signExpiredToken creates a PASETO v4 token that has already expired.
func signExpiredToken(sk pasetolib.V4AsymmetricSecretKey, workspaceID string) string {
	token := pasetolib.NewToken()
	token.SetIssuer("aureliomod")
	token.SetSubject("test-service")
	token.SetIssuedAt(time.Now().Add(-2 * time.Hour))
	token.SetNotBefore(time.Now().Add(-2 * time.Hour))
	token.SetExpiration(time.Now().Add(-1 * time.Hour))
	token.SetString("workspace_id", workspaceID)
	return token.V4Sign(sk, nil)
}

// newRequest creates a connect.Request with an Authorization header.
func newRequest(token string) *connect.Request[v1.AnalyzeRequest] {
	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-from-body",
		RawBytes:    []byte{0xFF},
	})
	if token != "" {
		req.Header().Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestInterceptor_ValidToken_PassesThrough(t *testing.T) {
	t.Setenv("PASETO_AUTH_ENABLED", "true")

	sk, pk := newTestKeypair()
	token := signTestToken(sk, "ws-valid", 1*time.Hour)

	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	resp, err := wrapped(t.Context(), newRequest(token))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !next.called {
		t.Fatal("next handler was not called")
	}

	// Verify workspace_id was propagated into context
	wsID, ok := WorkspaceIDFromContext(next.ctx)
	if !ok {
		t.Fatal("workspace_id not found in context")
	}
	if wsID != "ws-valid" {
		t.Errorf("workspace_id = %q, want %q", wsID, "ws-valid")
	}
	if resp.Any().(*v1.AnalyzeResponse).Decision != v1.Decision_DECISION_BLOCK {
		t.Error("response was not from the echo handler")
	}
}

func TestInterceptor_MissingHeader_ReturnsUnauthenticated(t *testing.T) {
	_, pk := newTestKeypair()

	t.Setenv("PASETO_AUTH_ENABLED", "true")

	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	// Request without Authorization header
	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF},
	})
	_, err := wrapped(t.Context(), req)
	requireUnauthenticated(t, err)
	if next.called {
		t.Error("next handler should NOT have been called")
	}
}

func TestInterceptor_InvalidToken_ReturnsUnauthenticated(t *testing.T) {
	t.Setenv("PASETO_AUTH_ENABLED", "true")

	_, pk := newTestKeypair()

	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	_, err := wrapped(t.Context(), newRequest("not.a.valid.token"))
	requireUnauthenticated(t, err)
	if next.called {
		t.Error("next handler should NOT have been called")
	}
}

func TestInterceptor_ExpiredToken_ReturnsUnauthenticated(t *testing.T) {
	t.Setenv("PASETO_AUTH_ENABLED", "true")

	sk, pk := newTestKeypair()
	token := signExpiredToken(sk, "ws-expired")

	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	_, err := wrapped(t.Context(), newRequest(token))
	requireUnauthenticated(t, err)
	if next.called {
		t.Error("next handler should NOT have been called")
	}
}

func TestInterceptor_WrongSigner_ReturnsUnauthenticated(t *testing.T) {
	t.Setenv("PASETO_AUTH_ENABLED", "true")

	// Create TWO different keypairs: one to sign, another for the interceptor
	signerSK, _ := newTestKeypair()
	_, verifierPK := newTestKeypair() // different keypair!

	token := signTestToken(signerSK, "ws-wrong", 1*time.Hour)

	interceptor := NewPASETOInterceptor(verifierPK)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	_, err := wrapped(t.Context(), newRequest(token))
	requireUnauthenticated(t, err)
	if next.called {
		t.Error("next handler should NOT have been called")
	}
}

func TestInterceptor_MissingWorkspaceIDClaim_ReturnsUnauthenticated(t *testing.T) {
	t.Setenv("PASETO_AUTH_ENABLED", "true")

	sk, pk := newTestKeypair()

	// Create a token WITHOUT workspace_id claim
	token := pasetolib.NewToken()
	token.SetIssuer("aureliomod")
	token.SetSubject("test-service")
	token.SetIssuedAt(time.Now())
	token.SetNotBefore(time.Now())
	token.SetExpiration(time.Now().Add(1 * time.Hour))
	signed := token.V4Sign(sk, nil)

	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	_, err := wrapped(t.Context(), newRequest(signed))
	requireUnauthenticated(t, err)
}

func TestInterceptor_MalformedAuthHeader_ReturnsUnauthenticated(t *testing.T) {
	t.Setenv("PASETO_AUTH_ENABLED", "true")

	_, pk := newTestKeypair()
	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	// Authorization header with "Basic" prefix instead of "Bearer"
	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-test",
		RawBytes:    []byte{0xFF},
	})
	req.Header().Set("Authorization", "Basic dXNlcjpwYXNz")

	_, err := wrapped(t.Context(), req)
	requireUnauthenticated(t, err)
}

func TestInterceptor_DisabledGate_GarbageTokenStillPasses(t *testing.T) {
	// Gate is disabled — even an obviously invalid token should pass through
	_, pk := newTestKeypair()

	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	// Sending literal "garbage" as a token — should NOT be validated
	_, err := wrapped(t.Context(), newRequest("garbage-token-that-should-be-ignored"))
	if err != nil {
		t.Fatalf("unexpected error with gate disabled: %v", err)
	}
	if !next.called {
		t.Fatal("next handler was not called (gate disabled should pass through)")
	}
}

func TestInterceptor_DisabledGate_PassesThrough(t *testing.T) {
	// PASETO_AUTH_ENABLED is NOT set (default=false)
	// No t.Setenv call — the env var should be absent

	_, pk := newTestKeypair()

	interceptor := NewPASETOInterceptor(pk)
	next := &echoNext{}
	wrapped := interceptor.WrapUnary(next.call)

	// Request with NO auth header — should pass through because gate is disabled
	req := connect.NewRequest(&v1.AnalyzeRequest{
		WorkspaceId: "ws-unauthed",
		RawBytes:    []byte{0xFF},
	})
	resp, err := wrapped(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error with gate disabled: %v", err)
	}
	if !next.called {
		t.Fatal("next handler was not called (gate disabled should pass through)")
	}
	if resp.Any().(*v1.AnalyzeResponse).Decision != v1.Decision_DECISION_BLOCK {
		t.Error("response was not from the echo handler")
	}
}
