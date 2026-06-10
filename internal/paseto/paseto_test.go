package paseto

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	tm, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if tm == nil {
		t.Fatal("New() returned nil")
	}
	if tm.publicKey.ExportHex() == "" {
		t.Error("public key is empty")
	}
}

func TestNewFromHex_Seed(t *testing.T) {
	// 64 hex chars = 32-byte seed
	tm, err := NewFromHex("e92551efe76b4095d38398292e040d3825b15b9bea263edaee702c6cdf9195d9")
	if err != nil {
		t.Fatalf("NewFromHex(seed) error: %v", err)
	}
	if tm.publicKey.ExportHex() == "" {
		t.Error("public key is empty")
	}
}

func TestNewFromHex_FullKey(t *testing.T) {
	// Generate a full key to get 128 hex chars
	tm1, _ := New()
	fullHex := tm1.SecretKeyHex()

	tm2, err := NewFromHex(fullHex)
	if err != nil {
		t.Fatalf("NewFromHex(full key) error: %v", err)
	}
	if tm2.PublicKeyHex() != tm1.PublicKeyHex() {
		t.Error("public keys differ after re-importing full key")
	}
}

func TestNewFromHex_InvalidLength(t *testing.T) {
	_, err := NewFromHex("bad")
	if err == nil {
		t.Fatal("expected error for invalid hex length")
	}
}

func TestServiceToken_RoundTrip(t *testing.T) {
	tm, _ := New()

	token, err := tm.ServiceToken("engine", 5*time.Minute)
	if err != nil {
		t.Fatalf("ServiceToken() error: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}

	parsed, err := tm.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken() error: %v", err)
	}

	sub, _ := parsed.GetSubject()
	if sub != "engine" {
		t.Errorf("subject = %q, want 'engine'", sub)
	}
}

func TestServiceToken_Expired(t *testing.T) {
	tm, _ := New()

	token, err := tm.ServiceToken("test", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("ServiceToken() error: %v", err)
	}

	time.Sleep(10 * time.Millisecond) // ensure token expires

	_, err = tm.VerifyToken(token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestServiceToken_DifferentService(t *testing.T) {
	tm1, _ := New()
	tm2, _ := New()

	token, _ := tm1.ServiceToken("engine", time.Minute)

	// tm2 has a different key — should reject tm1's token
	_, err := tm2.VerifyToken(token)
	if err == nil {
		t.Fatal("token from different key pair should not be valid")
	}
}

func TestPublicKeyExport(t *testing.T) {
	tm, _ := New()
	hex := tm.PublicKeyHex()
	if len(hex) != 64 {
		t.Errorf("PublicKeyHex length = %d, want 64", len(hex))
	}
}

func TestRotatableTokenManager_BothKeys(t *testing.T) {
	rtm, err := NewRotatable(
		"e92551efe76b4095d38398292e040d3825b15b9bea263edaee702c6cdf9195d9",
		"f92551efe76b4095d38398292e040d3825b15b9bea263edaee702c6cdf9195d9",
	)
	if err != nil {
		t.Fatalf("NewRotatable: %v", err)
	}

	token, err := rtm.ServiceToken("test", time.Minute)
	if err != nil {
		t.Fatalf("ServiceToken: %v", err)
	}

	parsed, err := rtm.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken with current key: %v", err)
	}
	sub, _ := parsed.GetSubject()
	if sub != "test" {
		t.Errorf("subject = %q, want 'test'", sub)
	}
}

func TestRotatableTokenManager_NoPrevious(t *testing.T) {
	rtm, err := NewRotatable(
		"e92551efe76b4095d38398292e040d3825b15b9bea263edaee702c6cdf9195d9",
		"",
	)
	if err != nil {
		t.Fatalf("NewRotatable: %v", err)
	}

	// previous must be nil
	if rtm.previous != nil {
		t.Error("expected previous to be nil when not provided")
	}

	token, _ := rtm.ServiceToken("test", time.Minute)
	_, err = rtm.VerifyToken(token)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
}
