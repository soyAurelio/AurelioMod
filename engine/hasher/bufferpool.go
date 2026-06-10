package hasher

import "sync"

// bufferPool reuses 32KB byte buffers across normalization operations
// to reduce GC pressure during streaming FFmpeg pipelines.
var bufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 32*1024)
		return &buf
	},
}

// getBuffer returns a pooled byte slice with at least minCap capacity.
func getBuffer(minCap int) *[]byte {
	buf := bufferPool.Get().(*[]byte)
	if cap(*buf) < minCap {
		*buf = make([]byte, 0, minCap)
	} else {
		*buf = (*buf)[:0]
	}
	return buf
}

// putBuffer returns a byte slice to the pool for reuse.
func putBuffer(buf *[]byte) {
	if buf != nil && cap(*buf) <= 1024*1024 { // don't pool huge buffers
		bufferPool.Put(buf)
	}
}
