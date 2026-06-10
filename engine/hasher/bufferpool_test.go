package hasher

import "testing"

func TestBufferPool_GetPut(t *testing.T) {
	buf := getBuffer(1024)
	if cap(*buf) < 1024 {
		t.Errorf("capacity = %d, want >= 1024", cap(*buf))
	}
	*buf = append(*buf, 'x') // use the buffer
	putBuffer(buf)

	buf2 := getBuffer(512)
	if cap(*buf2) < 512 {
		t.Errorf("second buffer should have capacity >= 512")
	}
	putBuffer(buf2)
}

func TestBufferPool_CapacityGrow(t *testing.T) {
	small := getBuffer(64)
	putBuffer(small)

	large := getBuffer(64 * 1024)
	if cap(*large) < 64*1024 {
		t.Errorf("capacity = %d, want >= 65536", cap(*large))
	}
	putBuffer(large)
}

func TestBufferPool_NilSafe(t *testing.T) {
	putBuffer(nil) // should not panic
}
