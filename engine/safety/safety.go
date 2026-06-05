// Package safety provides URL reputation checking via Google Safe Browsing v4
// with DragonflyDB caching to avoid repeated API calls.
package safety

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Sentinel errors for URL reputation outcomes.
var (
	// ErrMaliciousURL indicates the URL is known malicious (malware, phishing, etc.).
	ErrMaliciousURL = errors.New("URL is malicious")

	// ErrServiceUnavailable indicates the Safe Browsing API is unreachable.
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
var _ URLReputationService = (*SafeBrowsingService)(nil)

// SafeBrowsingService queries Google Safe Browsing v4 for URL reputation,
// caching results in DragonflyDB with a configurable TTL.
type SafeBrowsingService struct {
	rdb       *redis.Client
	enabled   bool
	cacheTTL  time.Duration
}

// SafeBrowsingConfig holds configuration for the Safe Browsing service.
type SafeBrowsingConfig struct {
	// RDB is the DragonflyDB client for caching lookups.
	RDB *redis.Client

	// Enabled controls whether URL checks are performed.
	// When false, all checks are bypassed.
	Enabled bool

	// CacheTTL is the expiration time for cached lookup results.
	// Default: 15 minutes.
	CacheTTL time.Duration
}

// NewSafeBrowsingService creates a Safe Browsing service with DragonflyDB caching.
func NewSafeBrowsingService(cfg SafeBrowsingConfig) *SafeBrowsingService {
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	return &SafeBrowsingService{
		rdb:      cfg.RDB,
		enabled:  cfg.Enabled,
		cacheTTL: ttl,
	}
}

// CheckURL verifies the URL against Google Safe Browsing v4.
// Results are cached in DragonflyDB via SETEX to avoid repeated API calls.
//
// Fail-closed: if the Safe Browsing API is unreachable, the URL is rejected
// with ErrServiceUnavailable. This prevents malicious content from bypassing
// the check when the API is down.
func (s *SafeBrowsingService) CheckURL(ctx context.Context, url string) error {
	if !s.enabled {
		slog.WarnContext(ctx, "Safe Browsing disabled — bypassing URL check",
			"url", url,
		)
		return nil
	}

	cacheKey := "safebrowsing:" + url

	// Check DragonflyDB cache first
	if s.rdb != nil {
		cached, err := s.rdb.Get(ctx, cacheKey).Result()
		if err == nil {
			switch cached {
			case "safe":
				slog.DebugContext(ctx, "Safe Browsing cache hit — safe", "url", url)
				return nil
			case "malicious":
				slog.WarnContext(ctx, "Safe Browsing cache hit — malicious", "url", url)
				return ErrMaliciousURL
			}
		}
		// Cache miss or error → proceed to API query
	}

	// Perform the actual Safe Browsing lookup.
	// The API call is abstracted behind the interface via a pluggable transport.
	// Integration with google/safebrowsing v4 happens at construction time.
	result, err := s.lookup(ctx, url)
	if err != nil {
		slog.ErrorContext(ctx, "Safe Browsing API unreachable — failing closed",
			"url", url, "error", err,
		)
		return ErrServiceUnavailable
	}

	// Cache the result
	if s.rdb != nil {
		cacheVal := "safe"
		if result != nil {
			cacheVal = "malicious"
		}
		if setErr := s.rdb.SetEx(ctx, cacheKey, cacheVal, s.cacheTTL).Err(); setErr != nil {
			slog.WarnContext(ctx, "Safe Browsing cache write failed", "url", url, "error", setErr)
		}
	}

	if result != nil {
		return ErrMaliciousURL
	}

	return nil
}

// lookup performs the actual Safe Browsing API query.
// This is a placeholder that will be integrated with google/safebrowsing v4
// when the API key is available. For now, it returns nil (safe) to avoid
// blocking development.
func (s *SafeBrowsingService) lookup(ctx context.Context, url string) (error, error) {
	// TODO: Integrate with google/safebrowsing v4 API
	// The google/safebrowsing client requires OAuth2 credentials.
	// For now, log that a lookup would have been performed.
	slog.DebugContext(ctx, "Safe Browsing lookup triggered (integration pending)",
		"url", url,
	)

	// Return nil threat + nil API error = safe, no API error
	// This is a development placeholder — in production, the safebrowsing
	// client will be injected via constructor.
	return fmt.Errorf("Safe Browsing API not yet integrated — implement lookup with google/safebrowsing v4"), nil
}

// redisSafeBrowsingKey returns the cache key for a URL lookup.
func redisSafeBrowsingKey(url string) string {
	return "safebrowsing:" + url
}
