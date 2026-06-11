// Package safety provides URL reputation checking via Google Web Risk API v1
// with DragonflyDB caching to avoid repeated API calls.
package safety

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	webrisk "cloud.google.com/go/webrisk/apiv1"
	webriskpb "cloud.google.com/go/webrisk/apiv1/webriskpb"
	"github.com/googleapis/gax-go/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/api/option"
)

// Sentinel errors for URL reputation outcomes.
var (
	// ErrMaliciousURL indicates the URL is known malicious (malware, phishing, etc.).
	ErrMaliciousURL = errors.New("URL is malicious")

	// ErrServiceUnavailable indicates the Web Risk API is unreachable.
	// The system fails-closed for safety.
	ErrServiceUnavailable = errors.New("URL safety check unavailable")
)

// URLReputationService checks whether a URL is safe to fetch content from.
type URLReputationService interface {
	// CheckURL returns nil if the URL is safe, or an error if it is malicious
	// or the safety check cannot be completed (fail-closed).
	CheckURL(ctx context.Context, url string) error
}

// Compile-time interface check.
var _ URLReputationService = (*WebRiskService)(nil)

// lookupClient abstracts the Web Risk API call for testability.
// webrisk.Client satisfies this interface directly.
type lookupClient interface {
	SearchUris(ctx context.Context, req *webriskpb.SearchUrisRequest,
		opts ...gax.CallOption) (*webriskpb.SearchUrisResponse, error)
}

// WebRiskService queries Google Web Risk API v1 for URL reputation,
// caching results in DragonflyDB with a configurable TTL.
type WebRiskService struct {
	rdb      *redis.Client
	client   lookupClient
	enabled  bool
	cacheTTL time.Duration
}

// WebRiskConfig holds configuration for the Web Risk service.
type WebRiskConfig struct {
	// RDB is the DragonflyDB client for caching lookups.
	RDB *redis.Client

	// Client is the Web Risk API client. If nil, one is auto-created via
	// newWebRiskClient using ADC or WEBRISK_API_KEY env var.
	Client lookupClient

	// Enabled controls whether URL checks are performed.
	// When false, all checks are bypassed.
	Enabled bool

	// CacheTTL is the default expiration time for cached lookup results
	// when the API response does not provide an expireTime.
	// Default: 15 minutes.
	CacheTTL time.Duration
}

// NewWebRiskService creates a Web Risk service with DragonflyDB caching.
// If cfg.Client is nil and cfg.Enabled is true, a client is auto-created
// via newWebRiskClient using ADC (default) or WEBRISK_API_KEY env var
// (fallback). When client creation fails, an error is returned to enable
// fail-fast startup detection.
//
// If cfg.Enabled is false, no client is needed — the service bypasses
// all URL checks.
func NewWebRiskService(ctx context.Context, cfg WebRiskConfig) (*WebRiskService, error) {
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	client := cfg.Client
	if client == nil && cfg.Enabled {
		var err error
		client, err = newWebRiskClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("webrisk client creation: %w", err)
		}
		if client == nil {
			return nil, errors.New("webrisk client creation returned nil")
		}
	}

	return &WebRiskService{
		rdb:      cfg.RDB,
		client:   client,
		enabled:  cfg.Enabled,
		cacheTTL: ttl,
	}, nil
}

// newWebRiskClient creates a Web Risk API client.
// Prefers explicit WEBRISK_API_KEY env var; falls back to ADC.
func newWebRiskClient(ctx context.Context) (lookupClient, error) {
	if key := os.Getenv("WEBRISK_API_KEY"); key != "" {
		return webrisk.NewClient(ctx, option.WithAPIKey(key))
	}
	return webrisk.NewClient(ctx)
}

// CheckURL verifies the URL against Google Web Risk API v1.
// Results are cached in DragonflyDB via SETEX to avoid repeated API calls.
//
// Fail-closed: if the Web Risk API is unreachable, the URL is rejected
// with ErrServiceUnavailable. This prevents malicious content from bypassing
// the check when the API is down.
func (s *WebRiskService) CheckURL(ctx context.Context, url string) error {
	if !s.enabled {
		slog.WarnContext(ctx, "Web Risk disabled — bypassing URL check",
			"url", url,
		)
		return nil
	}

	cacheKey := "webrisk:" + url

	// Check DragonflyDB cache first
	if s.rdb != nil {
		cached, err := s.rdb.Get(ctx, cacheKey).Result()
		if err == nil {
			switch cached {
			case "safe":
				slog.DebugContext(ctx, "Web Risk cache hit — safe", "url", url)
				return nil
			case "malicious":
				slog.WarnContext(ctx, "Web Risk cache hit — malicious", "url", url)
				return ErrMaliciousURL
			}
		}
		// Cache miss or error → proceed to API query
	}

	// Perform the actual Web Risk lookup.
	threat, err := s.searchUris(ctx, url)
	if err != nil {
		slog.ErrorContext(ctx, "Web Risk API unreachable — failing closed",
			"url", url, "error", err,
		)
		return ErrServiceUnavailable
	}

	// Compute cache TTL from server expireTime, capped at 24h.
	ttl := s.cacheTTL
	if threat != nil && threat.ExpireTime != nil {
		if serverTTL := time.Until(threat.ExpireTime.AsTime()); serverTTL > 0 {
			ttl = min(serverTTL, 24*time.Hour)
		}
	}

	// Cache the result
	if s.rdb != nil {
		cacheVal := "safe"
		if threat != nil {
			cacheVal = "malicious"
		}
		if setErr := s.rdb.SetEx(ctx, cacheKey, cacheVal, ttl).Err(); setErr != nil {
			slog.WarnContext(ctx, "Web Risk cache write failed", "url", url, "error", setErr)
		}
	}

	if threat != nil {
		return ErrMaliciousURL
	}

	return nil
}

// searchUris calls the Web Risk v1 uris.search method and returns the threat
// info if the URL is classified as malicious. Returns nil threat for clean URLs.
func (s *WebRiskService) searchUris(ctx context.Context, url string) (*webriskpb.SearchUrisResponse_ThreatUri, error) {
	resp, err := s.client.SearchUris(ctx, &webriskpb.SearchUrisRequest{
		Uri: url,
		ThreatTypes: []webriskpb.ThreatType{
			webriskpb.ThreatType_MALWARE,
			webriskpb.ThreatType_SOCIAL_ENGINEERING,
			webriskpb.ThreatType_UNWANTED_SOFTWARE,
		},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetThreat(), nil
}
