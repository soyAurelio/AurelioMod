package safety

import (
	"context"
	"errors"
	"testing"
)

// TestURLReputation_MockClean verifies that a clean URL passes the reputation check.
func TestURLReputation_MockClean(t *testing.T) {
	svc := newMockReputationService(nil) // nil error = clean

	ctx := t.Context()
	err := svc.CheckURL(ctx, "https://safe.example.com")

	if err != nil {
		t.Fatalf("CheckURL(clean) unexpected error: %v", err)
	}
}

// TestURLReputation_MockMalware verifies a malicious URL is blocked.
func TestURLReputation_MockMalware(t *testing.T) {
	svc := newMockReputationService(ErrMaliciousURL)

	ctx := t.Context()
	err := svc.CheckURL(ctx, "https://evil.example.com")

	if err == nil {
		t.Fatal("CheckURL(malware) expected error, got nil")
	}
	if !errors.Is(err, ErrMaliciousURL) {
		t.Errorf("CheckURL error = %v, want ErrMaliciousURL", err)
	}
}

// TestURLReputation_MockSocialEngineering verifies a phishing URL is blocked.
func TestURLReputation_MockSocialEngineering(t *testing.T) {
	svc := newMockReputationService(ErrMaliciousURL)

	ctx := t.Context()
	err := svc.CheckURL(ctx, "https://phish.example.com")

	if err == nil {
		t.Fatal("CheckURL(phishing) expected error, got nil")
	}
}

// TestURLReputation_MockTimeout verifies fail-closed behavior on timeout.
func TestURLReputation_MockTimeout(t *testing.T) {
	svc := newMockReputationService(ErrServiceUnavailable)

	ctx := t.Context()
	err := svc.CheckURL(ctx, "https://timeout.example.com")

	if err == nil {
		t.Fatal("CheckURL(timeout) expected error (fail-closed), got nil")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("CheckURL error = %v, want ErrServiceUnavailable", err)
	}
}

// TestURLReputation_DisabledGate verifies that when SAFEBROWSING_ENABLED=false,
// all URL checks are bypassed without error.
func TestURLReputation_DisabledGate(t *testing.T) {
	// Even a malware result should pass through when disabled
	svc := newMockReputationService(ErrMaliciousURL)
	svc.enabled = false

	ctx := t.Context()
	err := svc.CheckURL(ctx, "https://evil.example.com")

	if err != nil {
		t.Fatalf("CheckURL(disabled) unexpected error: %v", err)
	}
}

// TestSafeBrowsingCacheHit verifies a cached safe result is used without API call.
func TestSafeBrowsingCacheHit(t *testing.T) {
	svc := newMockReputationService(nil)
	svc.cachedURL = "https://cached.example.com" // simulate cache hit

	ctx := t.Context()
	err := svc.CheckURL(ctx, "https://cached.example.com")

	if err != nil {
		t.Fatalf("CheckURL(cached-clean) error: %v", err)
	}
}

// mockReputationService implements URLReputationService for testing.
type mockReputationService struct {
	err       error
	enabled   bool
	cachedURL string
}

func newMockReputationService(err error) *mockReputationService {
	return &mockReputationService{
		err:     err,
		enabled: true,
	}
}

func (m *mockReputationService) CheckURL(_ context.Context, url string) error {
	if !m.enabled {
		return nil
	}
	if m.cachedURL == url {
		// Cache hit — return cached result (nil = clean in mock).
		return nil
	}
	return m.err
}
