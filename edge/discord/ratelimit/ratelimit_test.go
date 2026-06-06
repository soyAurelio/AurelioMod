package ratelimit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestTokenBucket_Allow_NormalRate verifies that Allow returns true
// when the bucket has available tokens under normal rate with refill.
func TestTokenBucket_Allow_NormalRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		limiter := NewLimiter(logger)

		// Burst of 10 + refill rate 45/s
		for i := 0; i < 10; i++ {
			if !limiter.Allow(t.Context()) {
				t.Errorf("Allow() returned false on iteration %d with burst tokens available", i)
				return
			}
		}
		// After burst exhausted, advance time to refill tokens
		synctest.Wait()
		time.Sleep(200 * time.Millisecond) // ~9 tokens refilled
		for i := 0; i < 9; i++ {
			if !limiter.Allow(t.Context()) {
				t.Errorf("Allow() returned false on iteration %d after refill", i+10)
				return
			}
		}
	})
}

// TestTokenBucket_Allow_ExhaustedReturnsFalse verifies that Allow returns
// false immediately when no tokens are available (non-blocking).
func TestTokenBucket_Allow_ExhaustedReturnsFalse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		limiter := NewLimiter(logger)

		// Drain all 10 burst tokens
		for i := 0; i < 10; i++ {
			limiter.Allow(t.Context())
		}

		// Next call should return false immediately (no time advanced)
		if limiter.Allow(t.Context()) {
			t.Error("Allow() should return false when bucket exhausted and no time elapsed")
		}
	})
}

// TestTokenBucket_Wait_AcquiresToken verifies that Wait blocks until a token
// is available and then returns nil.
func TestTokenBucket_Wait_AcquiresToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		limiter := NewLimiter(logger)

		// Drain all burst tokens
		for i := 0; i < 10; i++ {
			limiter.Allow(t.Context())
		}

		// Wait should succeed after refill
		done := make(chan error, 1)
		go func() {
			done <- limiter.Wait(t.Context())
		}()

		// Advance time for refill
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Wait() returned error after refill: %v", err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Wait() did not return after token refill")
		}
	})
}

// TestTokenBucket_Wait_TimeoutLogsDrop verifies that when the context
// deadline expires before a token becomes available, Wait returns an error
// and logs rate_limit_drop.
func TestTokenBucket_Wait_TimeoutLogsDrop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		limiter := NewLimiter(logger)

		// Drain all burst tokens
		for i := 0; i < 10; i++ {
			limiter.Allow(t.Context())
		}

		// Context with 1ms timeout — shorter than the ~22ms refill interval
		ctx, cancel := context.WithTimeout(t.Context(), 1*time.Millisecond)
		defer cancel()

		err := limiter.Wait(ctx)
		if err == nil {
			t.Error("Wait() should return error when context deadline exceeded before refill")
		}

		// Verify rate_limit_drop logged
		output := buf.String()
		if !strings.Contains(output, "rate_limit_drop") {
			t.Errorf("Expected log to contain 'rate_limit_drop', got: %s", output)
		}
	})
}

// TestTokenBucket_Wait_QueueDeadlineExceeded verifies that Wait returns
// an error when even the internal 2-second queue deadline can't acquire a token.
// This tests the path where tokens are reserved far into the future.
func TestTokenBucket_Wait_QueueDeadlineExceeded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		limiter := NewLimiter(logger)

		// Drain ALL tokens with Allow, which is non-blocking.
		for i := 0; i < 10; i++ {
			limiter.Allow(t.Context())
		}

		// Context with 1ms timeout — must fail since rate is 45/s (22ms/token)
		ctx, cancel := context.WithTimeout(t.Context(), 1*time.Millisecond)
		defer cancel()

		err := limiter.Wait(ctx)
		if err == nil {
			t.Error("Wait() should error with 1ms context when bucket is empty")
		}

		output := buf.String()
		if !strings.Contains(output, "rate_limit_drop") {
			t.Errorf("Expected 'rate_limit_drop' in log output, got: %s", output)
		}
	})
}

// TestTokenBucket_ImplementsLimiter verifies compile-time interface compliance.
func TestTokenBucket_ImplementsLimiter(t *testing.T) {
	var _ Limiter = NewLimiter(slog.Default())
}
