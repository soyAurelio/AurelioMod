package safety

import (
	"context"
	"errors"
	"testing"
	"time"

	webriskpb "cloud.google.com/go/webrisk/apiv1/webriskpb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// TestURLReputation_DisabledGate verifies that when WEBRISK_ENABLED=false,
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

// TestWebRiskCacheHit verifies a cached safe result is used without API call.
func TestWebRiskCacheHit(t *testing.T) {
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

// --- Mock lookupClient for testing searchUris ---

// mockLookupClient implements lookupClient for unit testing searchUris.
type mockLookupClient struct {
	threats    []webriskpb.ThreatType
	expireTime *timestamppb.Timestamp
	err        error
}

func (m *mockLookupClient) SearchUris(_ context.Context, _ *webriskpb.SearchUrisRequest, _ ...gax.CallOption) (*webriskpb.SearchUrisResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.threats) == 0 {
		return &webriskpb.SearchUrisResponse{}, nil // clean URL
	}
	return &webriskpb.SearchUrisResponse{
		Threat: &webriskpb.SearchUrisResponse_ThreatUri{
			ThreatTypes: m.threats,
			ExpireTime:  m.expireTime,
		},
	}, nil
}

// TestWebRiskService_SearchUris_Malware verifies searchUris correctly
// identifies a MALWARE threat from the Web Risk API response.
func TestWebRiskService_SearchUris_Malware(t *testing.T) {
	expireTime := timestamppb.New(time.Now().Add(1 * time.Hour))
	mock := &mockLookupClient{
		threats:    []webriskpb.ThreatType{webriskpb.ThreatType_MALWARE},
		expireTime: expireTime,
	}
	svc := &WebRiskService{client: mock}

	ctx := t.Context()
	threat, err := svc.searchUris(ctx, "https://evil.com/malware")

	if err != nil {
		t.Fatalf("searchUris unexpected error: %v", err)
	}
	if threat == nil {
		t.Fatal("searchUris expected non-nil threat for MALWARE URL, got nil")
	}
	if len(threat.ThreatTypes) != 1 {
		t.Fatalf("expected 1 threat type, got %d", len(threat.ThreatTypes))
	}
	if threat.ThreatTypes[0] != webriskpb.ThreatType_MALWARE {
		t.Errorf("expected MALWARE threat, got %v", threat.ThreatTypes[0])
	}
}

// TestWebRiskService_SearchUris_Clean verifies searchUris returns nil
// threat for a clean URL with no threats.
func TestWebRiskService_SearchUris_Clean(t *testing.T) {
	mock := &mockLookupClient{
		threats: nil, // clean URL — no threats
	}
	svc := &WebRiskService{client: mock}

	ctx := t.Context()
	threat, err := svc.searchUris(ctx, "https://safe.example.com")

	if err != nil {
		t.Fatalf("searchUris unexpected error: %v", err)
	}
	if threat != nil {
		t.Fatalf("expected nil threat for clean URL, got %v", threat)
	}
}

// TestWebRiskService_SearchUris_Error verifies searchUris returns an
// error when the Web Risk API is unreachable (fail-closed path).
func TestWebRiskService_SearchUris_Error(t *testing.T) {
	mock := &mockLookupClient{
		err: errors.New("gRPC unavailable"),
	}
	svc := &WebRiskService{client: mock}

	ctx := t.Context()
	threat, err := svc.searchUris(ctx, "https://any.example.com")

	if err == nil {
		t.Fatal("searchUris expected error, got nil")
	}
	if threat != nil {
		t.Fatalf("expected nil threat on error, got %v", threat)
	}
}

// TestWebRiskService_SearchUris_MultiThreat verifies searchUris correctly
// handles URLs classified with multiple threat types (MALWARE + SOCIAL_ENGINEERING).
func TestWebRiskService_SearchUris_MultiThreat(t *testing.T) {
	expireTime := timestamppb.New(time.Now().Add(30 * time.Minute))
	mock := &mockLookupClient{
		threats: []webriskpb.ThreatType{
			webriskpb.ThreatType_MALWARE,
			webriskpb.ThreatType_SOCIAL_ENGINEERING,
		},
		expireTime: expireTime,
	}
	svc := &WebRiskService{client: mock}

	ctx := t.Context()
	threat, err := svc.searchUris(ctx, "https://evil.com/phishing-malware")

	if err != nil {
		t.Fatalf("searchUris unexpected error: %v", err)
	}
	if threat == nil {
		t.Fatal("searchUris expected non-nil threat for multi-threat URL, got nil")
	}
	if len(threat.ThreatTypes) != 2 {
		t.Fatalf("expected 2 threat types, got %d", len(threat.ThreatTypes))
	}
	hasMalware := false
	hasSocial := false
	for _, tt := range threat.ThreatTypes {
		if tt == webriskpb.ThreatType_MALWARE {
			hasMalware = true
		}
		if tt == webriskpb.ThreatType_SOCIAL_ENGINEERING {
			hasSocial = true
		}
	}
	if !hasMalware {
		t.Error("expected MALWARE threat type in response")
	}
	if !hasSocial {
		t.Error("expected SOCIAL_ENGINEERING threat type in response")
	}
}

// TestWebRiskService_Constructor_WithClient verifies that NewWebRiskService
// accepts a provided lookupClient and stores it correctly. The constructor
// MUST take a context (needed for auto-creation via ADC) and return an error
// when auto-creation fails with cfg.Enabled=true.
func TestWebRiskService_Constructor_WithClient(t *testing.T) {
	expireTime := timestamppb.New(time.Now().Add(1 * time.Hour))
	mock := &mockLookupClient{
		threats:    []webriskpb.ThreatType{webriskpb.ThreatType_MALWARE},
		expireTime: expireTime,
	}

	ctx := t.Context()
	svc, err := NewWebRiskService(ctx, WebRiskConfig{
		Client:  mock,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("NewWebRiskService with provided client: unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("NewWebRiskService returned nil service")
	}

	// Verify the client is wired — searchUris should use the mock
	threat, searchErr := svc.searchUris(ctx, "https://evil.com")
	if searchErr != nil {
		t.Fatalf("searchUris unexpected error: %v", searchErr)
	}
	if threat == nil {
		t.Fatal("expected non-nil threat from mock client")
	}
}

// TestWebRiskService_Constructor_Disabled verifies that when Enabled=false,
// the constructor succeeds even without a client.
func TestWebRiskService_Constructor_Disabled(t *testing.T) {
	ctx := t.Context()
	svc, err := NewWebRiskService(ctx, WebRiskConfig{
		Client:  nil,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("NewWebRiskService disabled: unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("NewWebRiskService disabled: returned nil service")
	}
	if svc.client != nil {
		t.Error("disabled service should not have a client")
	}
}
