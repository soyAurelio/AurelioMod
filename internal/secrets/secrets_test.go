package secrets

import "testing"

func TestEnvGetter_Get(t *testing.T) {
	g := NewEnv()
	t.Setenv("TEST_KEY", "testvalue")

	v, ok := g.Get("TEST_KEY")
	if !ok {
		t.Fatal("Get returned false for set key")
	}
	if v != "testvalue" {
		t.Errorf("value = %q, want 'testvalue'", v)
	}
}

func TestEnvGetter_GetMissing(t *testing.T) {
	g := NewEnv()
	_, ok := g.Get("NONEXISTENT_KEY_12345")
	if ok {
		t.Error("Get returned true for missing key")
	}
}

func TestEnvGetter_MustGet(t *testing.T) {
	g := NewEnv()
	t.Setenv("REQUIRED", "present")
	v := g.MustGet("REQUIRED")
	if v != "present" {
		t.Errorf("MustGet = %q, want 'present'", v)
	}
}

func TestEnvGetter_MustGetPanic(t *testing.T) {
	g := NewEnv()
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet did not panic for missing key")
		}
	}()
	g.MustGet("MISSING_KEY_99999")
}
