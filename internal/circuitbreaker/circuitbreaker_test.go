package circuitbreaker

import (
	"errors"
	"testing"
	"time"

	"github.com/failsafe-go/failsafe-go/bulkhead"
)

func TestExecutor_BulkheadBlocksConcurrent(t *testing.T) {
	cfg := DefaultExecutorConfig()
	cfg.MaxConcurrent = 1
	exec := NewExecutor[string](cfg)

	// Acquire the single permit and hold it.
	blocker := make(chan struct{})
	acquired := make(chan struct{})

	go func() {
		_, _ = exec.Get(func() (string, error) {
			close(acquired) // signal: we have the permit
			<-blocker       // hold until test releases
			return "first", nil
		})
	}()

	select {
	case <-acquired:
		// permit acquired
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first goroutine to acquire bulkhead permit")
	}

	// Second call must fail immediately — bulkhead has 1 permit, it's taken.
	_, err := exec.Get(func() (string, error) {
		return "second", nil
	})

	close(blocker) // release the first goroutine

	if err == nil {
		t.Fatal("expected bulkhead full error, got nil")
	}
	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull, got %v", err)
	}
}

func TestExecutor_BulkheadDisabled(t *testing.T) {
	cfg := DefaultExecutorConfig()
	cfg.MaxConcurrent = 0 // disabled
	exec := NewExecutor[string](cfg)

	// Both sequential calls must succeed when bulkhead is disabled.
	r1, err1 := exec.Get(func() (string, error) {
		return "ok", nil
	})
	if err1 != nil {
		t.Fatalf("unexpected error on first call: %v", err1)
	}
	if r1 != "ok" {
		t.Errorf("expected 'ok', got %q", r1)
	}

	r2, err2 := exec.Get(func() (string, error) {
		return "ok2", nil
	})
	if err2 != nil {
		t.Fatalf("unexpected error on second call: %v", err2)
	}
	if r2 != "ok2" {
		t.Errorf("expected 'ok2', got %q", r2)
	}
}

func TestExecutor_BulkheadHigherConcurrency(t *testing.T) {
	cfg := DefaultExecutorConfig()
	cfg.MaxConcurrent = 2
	exec := NewExecutor[string](cfg)

	blocker := make(chan struct{})
	acquired := make(chan struct{}, 2)

	// Take both permits.
	for range 2 {
		go func() {
			_, _ = exec.Get(func() (string, error) {
				acquired <- struct{}{}
				<-blocker
				return "ok", nil
			})
		}()
	}

	// Wait for both to acquire.
	for range 2 {
		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for bulkhead permit acquisition")
		}
	}

	// Third call with permits=2 fully taken must fail.
	_, err := exec.Get(func() (string, error) {
		return "third", nil
	})

	close(blocker)

	if err == nil {
		t.Fatal("expected bulkhead full error on third call (permits=2, both taken)")
	}
	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull, got %v", err)
	}
}

func TestExecutor_BulkheadDefaultEnabled(t *testing.T) {
	cfg := DefaultExecutorConfig()
	cfg.MaxConcurrent = 3
	exec := NewExecutor[string](cfg)

	blocker := make(chan struct{})
	acquired := make(chan struct{}, 3)

	for range 3 {
		go func() {
			_, _ = exec.Get(func() (string, error) {
				acquired <- struct{}{}
				<-blocker
				return "ok", nil
			})
		}()
	}

	for range 3 {
		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out — expected 3 permits")
		}
	}

	_, err := exec.Get(func() (string, error) {
		return "fourth", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (3 permits taken), got %v", err)
	}
}

func TestExecutor_RetrySucceeds(t *testing.T) {
	cfg := DefaultExecutorConfig()
	cfg.MaxConcurrent = 0
	cfg.MaxRetries = 3
	exec := NewExecutor[string](cfg)

	attempts := 0
	result, err := exec.Get(func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("transient error")
		}
		return "success", nil
	})

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got %q", result)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestExecutor_CircuitBreakerOpens(t *testing.T) {
	cfg := DefaultExecutorConfig()
	cfg.MaxConcurrent = 0
	cfg.CBFailureThreshold = 3
	cfg.CBDelay = 2 * time.Second // long enough to stay open across test
	exec := NewExecutor[string](cfg)

	fail := errors.New("api error")

	// Trip the breaker: 3 consecutive failures.
	for range 3 {
		_, _ = exec.Get(func() (string, error) {
			return "", fail
		})
	}

	// 4th call: circuit should be open now. Fallback returns error.
	_, err := exec.Get(func() (string, error) {
		return "should not reach", nil
	})
	if err == nil {
		t.Fatal("expected circuit breaker to be open, got nil error")
	}
	// Verify the inner function was NOT called (circuit blocked execution).
	// The error from the fallback wraps the circuit breaker error.
	if err.Error() == "api error" {
		t.Error("expected circuit breaker error, but got the inner function's error — circuit may have let execution through")
	}
}
