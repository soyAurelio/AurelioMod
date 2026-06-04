// Package circuitbreaker provides resilience patterns for external API calls
// via failsafe-go. All calls to WaveSpeed MUST go through a circuit breaker.
package circuitbreaker

import (
	"context"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// WaveSpeedExecutor returns a failsafe Executor pre-configured for WaveSpeed API calls.
//
// Policies (applied innermost → outermost):
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

	return failsafe.With[R](retry, cb, timeoutPolicy, fb)
}

// Execute runs fn through the executor with the given context.
func Execute[R any](ctx context.Context, executor failsafe.Executor[R], fn func() (R, error)) (R, error) {
	return executor.WithContext(ctx).Get(fn)
}
