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
	// Non-numeric WAVESPEED_MAX_CONCURRENT is ignored; falls through to default (3).
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "not-a-number")
	exec := WaveSpeedExecutor[string]()

	// With default 3 permits, 3 concurrent calls should succeed.
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
			t.Fatal("timed out — expected 3 permits from default bronze tier")
		}
	}

	// 4th call must fail — all 3 permits taken.
	_, err := exec.Get(func() (string, error) {
		return "fourth", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (3 permits taken), got %v", err)
	}
}

func TestWaveSpeedExecutor_BulkheadNegativeEnv(t *testing.T) {
	// Negative WAVESPEED_MAX_CONCURRENT is ignored; falls through to default (3).
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "-5")
	exec := WaveSpeedExecutor[string]()

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
			t.Fatal("timed out — negative env should default to bronze (3)")
		}
	}

	_, err := exec.Get(func() (string, error) {
		return "fourth", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (defaulted to 3 on negative env), got %v", err)
	}
}

func TestWaveSpeedExecutor_BulkheadDefaultEnabled(t *testing.T) {
	// Default: when no env vars are set, bulkhead(3) is active (bronze tier).
	exec := WaveSpeedExecutor[string]()

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
			t.Fatal("timed out — default should be 3 permits (bronze)")
		}
	}

	_, err := exec.Get(func() (string, error) {
		return "fourth", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (3 permits taken, bronze default), got %v", err)
	}
}

func TestWaveSpeedExecutor_PlanSilver(t *testing.T) {
	// WAVESPEED_PLAN=silver → 100 concurrent (no Bulkhead rejection at scale).
	t.Setenv("WAVESPEED_PLAN", "silver")
	exec := WaveSpeedExecutor[string]()

	// Verify 2 concurrent calls succeed without blocking.
	blocker := make(chan struct{})
	acquired := make(chan struct{}, 2)

	for range 2 {
		go func() {
			_, _ = exec.Get(func() (string, error) {
				acquired <- struct{}{}
				<-blocker
				return "ok", nil
			})
		}()
	}

	for range 2 {
		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out — silver plan should allow 100 concurrent")
		}
	}

	close(blocker)
}

func TestWaveSpeedExecutor_PlanGold(t *testing.T) {
	// WAVESPEED_PLAN=gold → 2000 concurrent.
	t.Setenv("WAVESPEED_PLAN", "gold")
	exec := WaveSpeedExecutor[string]()

	_, err := exec.Get(func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Errorf("gold plan should allow concurrent calls, got: %v", err)
	}
}

func TestWaveSpeedExecutor_PlanOverride(t *testing.T) {
	// WAVESPEED_MAX_CONCURRENT overrides WAVESPEED_PLAN.
	t.Setenv("WAVESPEED_PLAN", "gold")        // would be 2000
	t.Setenv("WAVESPEED_MAX_CONCURRENT", "5") // overrides to 5
	exec := WaveSpeedExecutor[string]()

	blocker := make(chan struct{})
	acquired := make(chan struct{}, 5)

	for range 5 {
		go func() {
			_, _ = exec.Get(func() (string, error) {
				acquired <- struct{}{}
				<-blocker
				return "ok", nil
			})
		}()
	}

	for range 5 {
		select {
		case <-acquired:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out — explicit override should give 5 permits")
		}
	}

	// 6th call must fail — explicit override is 5.
	_, err := exec.Get(func() (string, error) {
		return "sixth", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (override=5 permits taken), got %v", err)
	}
}

func TestWaveSpeedExecutor_PlanUnknown(t *testing.T) {
	// Unknown plan falls through to default (3).
	t.Setenv("WAVESPEED_PLAN", "enterprise")
	exec := WaveSpeedExecutor[string]()

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
			t.Fatal("timed out — unknown plan should default to bronze (3)")
		}
	}

	_, err := exec.Get(func() (string, error) {
		return "fourth", nil
	})
	close(blocker)

	if !errors.Is(err, bulkhead.ErrFull) {
		t.Errorf("expected bulkhead.ErrFull (unknown plan defaults to 3), got %v", err)
	}
}

func TestWaveSpeedExecutor_PlanCaseInsensitive(t *testing.T) {
	// Plan names are case-insensitive.
	t.Setenv("WAVESPEED_PLAN", "SILVER")
	exec := WaveSpeedExecutor[string]()

	_, err := exec.Get(func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Errorf("SILVER plan should work case-insensitively, got: %v", err)
	}
}
