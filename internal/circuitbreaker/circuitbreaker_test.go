package circuitbreaker

import (
	"errors"
	"testing"
	"time"

	"github.com/failsafe-go/failsafe-go/bulkhead"
)

func TestWaveSpeedExecutor_BulkheadBlocksConcurrent(t *testing.T) {
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "1")
	exec := WaveSpeedExecutor[string]()

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

func TestWaveSpeedExecutor_BulkheadDisabled(t *testing.T) {
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "0")
	exec := WaveSpeedExecutor[string]()

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

func TestWaveSpeedExecutor_BulkheadHigherConcurrency(t *testing.T) {
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "2")
	exec := WaveSpeedExecutor[string]()

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

func TestWaveSpeedExecutor_BulkheadInvalidEnv(t *testing.T) {
	// Non-numeric env value must default to 1 (safe serialization).
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "not-a-number")
	exec := WaveSpeedExecutor[string]()

	blocker := make(chan struct{})
	acquired := make(chan struct{})

	go func() {
		_, _ = exec.Get(func() (string, error) {
			close(acquired)
			<-blocker
			return "first", nil
		})
	}()

	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for permit with invalid env")
	}

	_, err := exec.Get(func() (string, error) {
		return "second", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (defaulted to 1 on invalid env), got %v", err)
	}
}

func TestWaveSpeedExecutor_BulkheadNegativeEnv(t *testing.T) {
	// Negative value must default to 1.
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "-5")
	exec := WaveSpeedExecutor[string]()

	blocker := make(chan struct{})
	acquired := make(chan struct{})

	go func() {
		_, _ = exec.Get(func() (string, error) {
			close(acquired)
			<-blocker
			return "first", nil
		})
	}()

	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for permit with negative env")
	}

	_, err := exec.Get(func() (string, error) {
		return "second", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (defaulted to 1 on negative env), got %v", err)
	}
}

func TestWaveSpeedExecutor_BulkheadDefaultEnabled(t *testing.T) {
	// Default: when WAVESPEED_MAX_CONCURRENT is unset, bulkhead(1) is active.
	exec := WaveSpeedExecutor[string]()

	blocker := make(chan struct{})
	acquired := make(chan struct{})

	go func() {
		_, _ = exec.Get(func() (string, error) {
			close(acquired)
			<-blocker
			return "first", nil
		})
	}()

	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first goroutine to acquire default bulkhead permit")
	}

	_, err := exec.Get(func() (string, error) {
		return "second", nil
	})
	close(blocker)

	if err == nil {
		t.Fatal("expected bulkhead full error with default env (unset), got nil")
	}
	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull, got %v", err)
	}
}
