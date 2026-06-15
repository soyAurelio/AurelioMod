// Package circuitbreaker provides resilience patterns for external API calls
// via failsafe-go. Used for external model inference and API services.
package circuitbreaker

import (
	"context"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/bulkhead"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/fallback"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/failsafe-go/failsafe-go/timeout"
)

// ExecutorConfig holds configuration for a resilient API executor.
type ExecutorConfig struct {
	// MaxConcurrent is the Bulkhead limit. 0 = no Bulkhead (unlimited).
	MaxConcurrent int

	// MaxRetries is the number of retry attempts (default: 3).
	MaxRetries int

	// RetryBackoff is the initial backoff duration.
	RetryBackoff time.Duration

	// RetryMaxBackoff caps the exponential backoff.
	RetryMaxBackoff time.Duration

	// CBFailureThreshold opens the circuit breaker after N failures (default: 5).
	CBFailureThreshold int

	// CBFailureWindow is the rolling window for counting failures.
	CBFailureWindow time.Duration

	// CBDelay is how long the circuit stays open before half-open (default: 30s).
	CBDelay time.Duration

	// Timeout per attempt.
	Timeout time.Duration
}

// DefaultExecutorConfig returns production-recommended settings.
func DefaultExecutorConfig() ExecutorConfig {
	return ExecutorConfig{
		MaxConcurrent:      3,
		MaxRetries:         3,
		RetryBackoff:       100 * time.Millisecond,
		RetryMaxBackoff:    400 * time.Millisecond,
		CBFailureThreshold: 5,
		CBFailureWindow:    60 * time.Second,
		CBDelay:            30 * time.Second,
		Timeout:            10 * time.Second,
	}
}

// NewExecutor returns a failsafe Executor with retry, circuit breaker,
// timeout, fallback, and optional bulkhead policies.
//
// Policies (applied outermost → innermost):
//   - Bulkhead(N): gates concurrent calls, rejects immediately when full
//   - Retry: N attempts with exponential backoff
//   - Circuit Breaker: N failures → open for duration
//   - Timeout: per-attempt deadline
//   - Fallback: returns zero value if all policies exhausted
func NewExecutor[R any](cfg ExecutorConfig) failsafe.Executor[R] {
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	backoff := cfg.RetryBackoff
	if backoff <= 0 {
		backoff = 100 * time.Millisecond
	}
	maxBackoff := cfg.RetryMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 400 * time.Millisecond
	}

	retry := retrypolicy.NewBuilder[R]().
		WithMaxAttempts(maxRetries).
		WithBackoff(backoff, maxBackoff).
		Build()

	cbFail := cfg.CBFailureThreshold
	if cbFail <= 0 {
		cbFail = 5
	}
	cbWindow := cfg.CBFailureWindow
	if cbWindow <= 0 {
		cbWindow = 60 * time.Second
	}
	cbDelay := cfg.CBDelay
	if cbDelay <= 0 {
		cbDelay = 30 * time.Second
	}

	cb := circuitbreaker.NewBuilder[R]().
		WithFailureThreshold(uint(cbFail)).
		WithFailureThresholdPeriod(uint(cbFail), cbWindow).
		WithDelay(cbDelay).
		Build()

	timeoutDuration := cfg.Timeout
	if timeoutDuration <= 0 {
		timeoutDuration = 10 * time.Second
	}
	timeoutPolicy := timeout.NewBuilder[R](timeoutDuration).Build()

	fb := fallback.NewBuilderWithFunc[R](func(exec failsafe.Execution[R]) (R, error) {
		return *new(R), exec.LastError()
	}).Build()

	policies := []failsafe.Policy[R]{retry, cb, timeoutPolicy, fb}

	if cfg.MaxConcurrent > 0 {
		bh := bulkhead.NewBuilder[R](uint(cfg.MaxConcurrent)).
			WithMaxWaitTime(0). // reject immediately when full
			Build()
		policies = append([]failsafe.Policy[R]{bh}, policies...)
	}

	return failsafe.With[R](policies...)
}

// Execute runs fn through the executor with the given context.
func Execute[R any](ctx context.Context, executor failsafe.Executor[R], fn func() (R, error)) (R, error) {
	return executor.WithContext(ctx).Get(fn)
}
