package secrets

import (
	"errors"
	"testing"
)

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

func TestEnvGetter_ErrSecretNotFound(t *testing.T) {
	_, ok := NewEnv().Get("MISSING_KEY_99999")
	if ok {
		t.Error("expected missing key")
	}
	if !errors.Is(ErrSecretNotFound, ErrSecretNotFound) {
		t.Error("ErrSecretNotFound sentinel not defined")
	}
}
