// Package circuitbreaker provides resilience patterns for external API calls
// via failsafe-go. All calls to WaveSpeed MUST go through a circuit breaker.
package circuitbreaker

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/bulkhead"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// WaveSpeedExecutor returns a failsafe Executor pre-configured for WaveSpeed API calls.
//
// Env gate: WAVESPEED_MAX_CONCURRENT controls the Bulkhead permit count (default 1).
// A value of 0 disables the Bulkhead entirely.
//
// Policies (applied outermost → innermost):
//   - Bulkhead(1): serializes all WaveSpeed calls, rejects immediately when busy
//   - Retry: 3 attempts, backoff 100ms → 200ms → 400ms
//   - Circuit Breaker: 5 failures → open 30s
//   - Timeout: 10s per attempt
//   - Fallback: returns zero value if all policies exhausted
func WaveSpeedExecutor[R any]() failsafe.Executor[R] {
	retry := retrypolicy.NewBuilder[R]().
		WithMaxAttempts(3).
		WithBackoff(100*time.Millisecond, 400*time.Millisecond).
		Build()

	cb := circuitbreaker.NewBuilder[R]().
		WithFailureThreshold(5).
		WithFailureThresholdPeriod(5, 60*time.Second).
		WithDelay(30 * time.Second).
		Build()

	timeoutPolicy := timeout.NewBuilder[R](10 * time.Second).Build()

	fb := fallback.NewBuilderWithFunc[R](func(exec failsafe.Execution[R]) (R, error) {
		return *new(R), exec.LastError()
	}).Build()

	policies := []failsafe.Policy[R]{retry, cb, timeoutPolicy, fb}

	// Bulkhead(1) serializes WaveSpeed calls to prevent overload storms.
	// Gate: WAVESPEED_MAX_CONCURRENT=0 disables the bulkhead entirely.
	if mc := parseMaxConcurrent(); mc > 0 {
		bh := bulkhead.NewBuilder[R](uint(mc)).
			WithMaxWaitTime(0). // reject immediately when full, don't queue
			Build()
		// Bulkhead is outermost: gate entry before any other policy runs.
		policies = append([]failsafe.Policy[R]{bh}, policies...)
	}

	return failsafe.With[R](policies...)
}

// parseMaxConcurrent reads WAVESPEED_MAX_CONCURRENT from env.
// Returns 1 if unset or unparseable (safe default: serial execution).
func parseMaxConcurrent() int {
	v := os.Getenv("WAVESPEED_MAX_CONCURRENT")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 1 // invalid or negative → default
	}
	return n
}

// Execute runs fn through the executor with the given context.
func Execute[R any](ctx context.Context, executor failsafe.Executor[R], fn func() (R, error)) (R, error) {
	return executor.WithContext(ctx).Get(fn)
}
