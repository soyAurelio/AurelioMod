package cache

import (
	"context"
	"testing"
	"time"

	aureliomodv1 "github.com/soyAurelio/AurelioMod/proto/aureliomod/v1"
)

// Compile-time checks: these mocks MUST satisfy the interfaces.
var (
	_ L1Cache = (*mockL1)(nil)
	_ L2Cache = (*mockL2)(nil)
)

// --- mock implementations for interface verification ---

type mockL1 struct {
	getFn func(ctx context.Context, hash string) (*CachedDecision, bool)
	setFn func(ctx context.Context, hash string, d *CachedDecision) error
}

func (m *mockL1) GetL1(ctx context.Context, hash string) (*CachedDecision, bool) {
	if m.getFn != nil {
		return m.getFn(ctx, hash)
	}
	return nil, false
}

func (m *mockL1) SetL1(ctx context.Context, hash string, d *CachedDecision) error {
	if m.setFn != nil {
		return m.setFn(ctx, hash, d)
	}
	return nil
}

type mockL2 struct {
	getFn func(ctx context.Context, pHash uint64, threshold int) ([]*CachedDecision, error)
	setFn func(ctx context.Context, pHash uint64, d *CachedDecision) error
}

func (m *mockL2) GetL2(ctx context.Context, pHash uint64, threshold int) ([]*CachedDecision, error) {
	if m.getFn != nil {
		return m.getFn(ctx, pHash, threshold)
	}
	return nil, nil
}

func (m *mockL2) SetL2(ctx context.Context, pHash uint64, d *CachedDecision) error {
	if m.setFn != nil {
		return m.setFn(ctx, pHash, d)
	}
	return nil
}

// --- behavioral tests ---

func TestCachedDecision_Struct(t *testing.T) {
	now := time.Now().UTC()
	d := CachedDecision{
		Decision:   aureliomodv1.Decision_DECISION_BLOCK,
		Confidence: 0.94,
		Category:   "violence_graphic",
		CachedAt:   now,
	}

	if d.Decision != aureliomodv1.Decision_DECISION_BLOCK {
		t.Errorf("Decision = %v, want DECISION_BLOCK", d.Decision)
	}
	if d.Confidence != 0.94 {
		t.Errorf("Confidence = %v, want 0.94", d.Confidence)
	}
	if d.Category != "violence_graphic" {
		t.Errorf("Category = %q, want violence_graphic", d.Category)
	}
	if !d.CachedAt.Equal(now) {
		t.Errorf("CachedAt = %v, want %v", d.CachedAt, now)
	}
}

func TestCachedDecision_ZeroValue(t *testing.T) {
	d := CachedDecision{}
	if d.CachedAt.IsZero() {
		t.Log("CachedAt zero value is the zero time — expected for new struct")
	}
}

func TestMockL1Cache_InterfaceSatisfaction(t *testing.T) {
	t.Run("hit returns decision", func(t *testing.T) {
		expected := &CachedDecision{
			Decision:   aureliomodv1.Decision_DECISION_ALLOW,
			Confidence: 0.99,
			Category:   "safe",
			CachedAt:   time.Now().UTC(),
		}
		mock := &mockL1{
			getFn: func(_ context.Context, _ string) (*CachedDecision, bool) {
				return expected, true
			},
		}

		got, ok := mock.GetL1(t.Context(), "deadbeef")
		if !ok {
			t.Fatal("expected cache hit")
		}
		if got != expected {
			t.Errorf("got %+v, want %+v", got, expected)
		}
	})

	t.Run("miss returns false", func(t *testing.T) {
		mock := &mockL1{
			getFn: func(_ context.Context, _ string) (*CachedDecision, bool) {
				return nil, false
			},
		}

		_, ok := mock.GetL1(t.Context(), "deadbeef")
		if ok {
			t.Fatal("expected cache miss")
		}
	})
}

func TestMockL2Cache_InterfaceSatisfaction(t *testing.T) {
	t.Run("hit returns nearest match", func(t *testing.T) {
		expected := []*CachedDecision{{
			Decision:   aureliomodv1.Decision_DECISION_BLOCK,
			Confidence: 0.87,
			Category:   "violence",
			CachedAt:   time.Now().UTC(),
		}}
		mock := &mockL2{
			getFn: func(_ context.Context, _ uint64, _ int) ([]*CachedDecision, error) {
				return expected, nil
			},
		}

		got, err := mock.GetL2(t.Context(), 0xDEAD, 5)
		if err != nil {
			t.Fatalf("GetL2 error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}
		if got[0].Decision != expected[0].Decision {
			t.Errorf("Decision = %v, want %v", got[0].Decision, expected[0].Decision)
		}
	})

	t.Run("empty results is valid", func(t *testing.T) {
		mock := &mockL2{
			getFn: func(_ context.Context, _ uint64, _ int) ([]*CachedDecision, error) {
				return []*CachedDecision{}, nil
			},
		}

		got, err := mock.GetL2(t.Context(), 0xDEAD, 5)
		if err != nil {
			t.Fatalf("GetL2 error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 results, got %d", len(got))
		}
	})
}
