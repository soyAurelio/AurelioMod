// Package circuitbreaker provides resilience patterns for external API calls
// via failsafe-go. All calls to WaveSpeed MUST go through a circuit breaker.
package circuitbreaker

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/bulkhead"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// waveSpeedPlanConcurrency maps WaveSpeed account tiers to their
// concurrent task limits (source: https://wavespeed.ai/docs/account-levels).
//
// Tiers are unlocked by cumulative top-up:
//   bronze — free, default for new accounts
//   silver — $100 total top-up
//   gold   — $1,000 total top-up
//   ultra  — $10,000 total top-up
var waveSpeedPlanConcurrency = map[string]int{
	"free":   3,    // same as bronze (conservative)
	"bronze": 3,
	"silver": 100,
	"gold":   2000,
	"ultra":  5000,
}

// WaveSpeedExecutor returns a failsafe Executor pre-configured for WaveSpeed API calls.
//
// Concurrency is resolved dynamically via resolveMaxConcurrent():
//   1. WAVESPEED_MAX_CONCURRENT=N → explicit override (0 = disable Bulkhead)
//   2. WAVESPEED_PLAN=tier        → look up in waveSpeedPlanConcurrency map
//   3. Default                     → 3 (bronze tier — default for new accounts)
//
// Policies (applied outermost → innermost):
//   - Bulkhead(N): gates concurrent WaveSpeed calls, rejects immediately when full
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

	// Bulkhead gates concurrent WaveSpeed calls.
	// resolveMaxConcurrent() → plan-based tier limit with explicit env override.
	// A value of 0 disables the Bulkhead entirely.
	if mc := resolveMaxConcurrent(); mc > 0 {
		bh := bulkhead.NewBuilder[R](uint(mc)).
			WithMaxWaitTime(0). // reject immediately when full, don't queue
			Build()
		// Bulkhead is outermost: gate entry before any other policy runs.
		policies = append([]failsafe.Policy[R]{bh}, policies...)
	}

	return failsafe.With[R](policies...)
}

// resolveMaxConcurrent determines the Bulkhead concurrency limit.
// Resolution order:
//   1. WAVESPEED_MAX_CONCURRENT=N → explicit number (0 = disable Bulkhead)
//   2. WAVESPEED_PLAN=tier        → preset from waveSpeedPlanConcurrency
//   3. Default                     → 3 (bronze tier — default for new accounts)
//
// This allows changing the limit without code changes: switch plan tiers
// as you top up, or override with an exact number.
func resolveMaxConcurrent() int {
	// 1. Explicit numeric override (highest priority)
	if v := os.Getenv("WAVESPEED_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}

	// 2. Plan-based tier (WAVESPEED_PLAN=bronze|silver|gold|ultra)
	if plan := strings.ToLower(os.Getenv("WAVESPEED_PLAN")); plan != "" {
		if n, ok := waveSpeedPlanConcurrency[plan]; ok {
			return n
		}
	}

	// 3. Default: bronze tier (free for new accounts, 3 concurrent tasks)
	return 3
}

// Execute runs fn through the executor with the given context.
func Execute[R any](ctx context.Context, executor failsafe.Executor[R], fn func() (R, error)) (R, error) {
	return executor.WithContext(ctx).Get(fn)
}
