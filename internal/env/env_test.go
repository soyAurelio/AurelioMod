package env

import "testing"

func TestGet(t *testing.T) {
	t.Setenv("TEST_KEY", "hello")
	if v := Get("TEST_KEY", "fallback"); v != "hello" {
		t.Errorf("Get = %q, want 'hello'", v)
	}
}

func TestGetFallback(t *testing.T) {
	if v := Get("MISSING", "default"); v != "default" {
		t.Errorf("Get = %q, want 'default'", v)
	}
}

func TestGetEmpty(t *testing.T) {
	t.Setenv("EMPTY", "")
	if v := Get("EMPTY", "fallback"); v != "fallback" {
		t.Errorf("Get empty = %q, want 'fallback'", v)
	}
}
