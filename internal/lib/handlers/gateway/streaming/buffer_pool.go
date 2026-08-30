// Package streaming provides high-performance streaming utilities for the gateway.
package streaming

import (
	"sync"
	"sync/atomic"
)

const (
	// DefaultBufferSize is the default size for pooled buffers (32KB).
	// This is optimized for typical SSE chunk batches.
	DefaultBufferSize = 32 * 1024

	// SmallBufferSize is for smaller allocations (4KB).
	SmallBufferSize = 4 * 1024

	// LargeBufferSize is for larger allocations (128KB).
	LargeBufferSize = 128 * 1024
)

// BufferPool manages reusable byte buffers to reduce GC pressure.
// It provides multiple pool sizes for different use cases.
type BufferPool struct {
	small  sync.Pool // 4KB buffers
	medium sync.Pool // 32KB buffers (default)
	large  sync.Pool // 128KB buffers

	// Stats for observability
	smallGets  atomic.Uint64
	smallPuts  atomic.Uint64
	mediumGets atomic.Uint64
	mediumPuts atomic.Uint64
	largeGets  atomic.Uint64
	largePuts  atomic.Uint64
}

// defaultPool is the global buffer pool instance.
var defaultPool = NewBufferPool()

// NewBufferPool creates a new buffer pool with pre-sized allocators.
func NewBufferPool() *BufferPool {
	return &BufferPool{
		small: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, SmallBufferSize)
				return &buf
			},
		},
		medium: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, DefaultBufferSize)
				return &buf
			},
		},
		large: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, LargeBufferSize)
				return &buf
			},
		},
	}
}

// GetBuffer returns a 32KB buffer from the pool.
func GetBuffer() *[]byte {
	defaultPool.mediumGets.Add(1)
	return defaultPool.medium.Get().(*[]byte)
}

// PutBuffer returns a buffer to the pool.
// The buffer is reset to zero length but retains its capacity.
func PutBuffer(b *[]byte) {
	if b == nil {
		return
	}
	// Reset length but keep capacity
	*b = (*b)[:0]
	defaultPool.mediumPuts.Add(1)
	defaultPool.medium.Put(b)
}

// GetSmallBuffer returns a 4KB buffer from the pool.
func GetSmallBuffer() *[]byte {
	defaultPool.smallGets.Add(1)
	return defaultPool.small.Get().(*[]byte)
}

// PutSmallBuffer returns a small buffer to the pool.
func PutSmallBuffer(b *[]byte) {
	if b == nil {
		return
	}
	*b = (*b)[:0]
	defaultPool.smallPuts.Add(1)
	defaultPool.small.Put(b)
}

// GetLargeBuffer returns a 128KB buffer from the pool.
func GetLargeBuffer() *[]byte {
	defaultPool.largeGets.Add(1)
	return defaultPool.large.Get().(*[]byte)
}

// PutLargeBuffer returns a large buffer to the pool.
func PutLargeBuffer(b *[]byte) {
	if b == nil {
		return
	}
	*b = (*b)[:0]
	defaultPool.largePuts.Add(1)
	defaultPool.large.Put(b)
}

// GetBufferSized returns a buffer of at least the specified size.
// It automatically selects the appropriate pool tier.
func GetBufferSized(size int) *[]byte {
	switch {
	case size <= SmallBufferSize:
		return GetSmallBuffer()
	case size <= DefaultBufferSize:
		return GetBuffer()
	default:
		return GetLargeBuffer()
	}
}

// PutBufferSized returns a buffer to the appropriate pool based on its capacity.
func PutBufferSized(b *[]byte) {
	if b == nil {
		return
	}
	switch cap(*b) {
	case SmallBufferSize:
		PutSmallBuffer(b)
	case DefaultBufferSize:
		PutBuffer(b)
	case LargeBufferSize:
		PutLargeBuffer(b)
	default:
		// Non-standard size, just let GC handle it
	}
}

// PoolStats contains buffer pool statistics.
type PoolStats struct {
	SmallGets  uint64
	SmallPuts  uint64
	MediumGets uint64
	MediumPuts uint64
	LargeGets  uint64
	LargePuts  uint64
}

// Stats returns current pool statistics.
func Stats() PoolStats {
	return PoolStats{
		SmallGets:  defaultPool.smallGets.Load(),
		SmallPuts:  defaultPool.smallPuts.Load(),
		MediumGets: defaultPool.mediumGets.Load(),
		MediumPuts: defaultPool.mediumPuts.Load(),
		LargeGets:  defaultPool.largeGets.Load(),
		LargePuts:  defaultPool.largePuts.Load(),
	}
}

// ResetStats resets all pool statistics.
func ResetStats() {
	defaultPool.smallGets.Store(0)
	defaultPool.smallPuts.Store(0)
	defaultPool.mediumGets.Store(0)
	defaultPool.mediumPuts.Store(0)
	defaultPool.largeGets.Store(0)
	defaultPool.largePuts.Store(0)
}

// ByteSlicePool is a simple wrapper for sync.Pool that provides []byte buffers.
// This is useful when you need a custom-sized pool.
type ByteSlicePool struct {
	pool sync.Pool
	size int
}

// NewByteSlicePool creates a new pool for buffers of the specified size.
func NewByteSlicePool(size int) *ByteSlicePool {
	return &ByteSlicePool{
		size: size,
		pool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, size)
				return &buf
			},
		},
	}
}

// Get returns a buffer from the pool.
func (p *ByteSlicePool) Get() *[]byte {
	return p.pool.Get().(*[]byte)
}

// Put returns a buffer to the pool.
func (p *ByteSlicePool) Put(b *[]byte) {
	if b == nil {
		return
	}
	*b = (*b)[:0]
	p.pool.Put(b)
}

// Size returns the buffer size for this pool.
func (p *ByteSlicePool) Size() int {
	return p.size
}

