// Package ratelimit provides a token bucket rate limiter for Discord API
// calls. It enforces a 45 req/s limit (headroom below Discord's 50 req/s
// hard cap) with a burst capacity of 10 and a 2-second wait queue.
// Exceeding the rate logs an event=rate_limit_drop via slog.
package ratelimit

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/time/rate"
)

// Limiter controls the rate of outgoing Discord API requests.
// Implementations must be safe for concurrent use.
type Limiter interface {
	// Allow returns true if a token can be acquired immediately without blocking.
	Allow(ctx context.Context) bool

	// Wait blocks until a token becomes available or the context is cancelled.
	// Internally enforces a 2-second queue deadline and logs rate_limit_drop
	// on overflow.
	Wait(ctx context.Context) error
}

// tokenBucket implements Limiter using golang.org/x/time/rate.
// Configuration: 45 req/s sustained, burst 10, 2-second queue timeout.
type tokenBucket struct {
	limiter *rate.Limiter
	logger  *slog.Logger
}

// NewLimiter creates a new Limiter configured for Discord API rate limits
// (45 req/s, burst 10). The logger is used to log rate_limit_drop events
// when the 2-second queue deadline is exceeded.
func NewLimiter(logger *slog.Logger) Limiter {
	return &tokenBucket{
		limiter: rate.NewLimiter(45, 10),
		logger:  logger,
	}
}

// Allow performs a non-blocking token check. Returns true if a token is
// available immediately, false otherwise. No rate_limit_drop is logged
// here — the caller may retry with Wait().
func (b *tokenBucket) Allow(ctx context.Context) bool {
	return b.limiter.Allow()
}

// Wait blocks until a token becomes available. If the wait exceeds a
// 2-second deadline or the context is cancelled, it logs event=rate_limit_drop
// and returns the error.
func (b *tokenBucket) Wait(ctx context.Context) error {
	// Enforce 2-second queue deadline
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	err := b.limiter.Wait(ctx)
	if err != nil {
		b.logger.WarnContext(ctx, "rate_limit_drop",
			slog.String("event", "rate_limit_drop"),
			slog.String("reason", "queue_timeout"),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}
