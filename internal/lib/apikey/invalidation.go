package apikey

import (
	"sync"
	"sync/atomic"
	"time"
)

const RestrictedCacheTTL = 30 * time.Second

var cacheInvalidation struct {
	sync.RWMutex
	revision  atomic.Uint64
	listeners []func(hash, keyID string)
}

// RegisterCacheInvalidator connects a process-lifetime authentication cache.
// Callbacks must only evict entries, never call back into this registry.
func RegisterCacheInvalidator(invalidate func(hash, keyID string)) {
	cacheInvalidation.Lock()
	defer cacheInvalidation.Unlock()
	cacheInvalidation.listeners = append(cacheInvalidation.listeners, invalidate)
}

// InvalidateCaches evicts matching hash or key id on this process. Revision
// fencing prevents a concurrent, older DB lookup from repopulating the cache.
func InvalidateCaches(hash, keyID string) {
	if hash == "" && keyID == "" {
		return
	}
	cacheInvalidation.Lock()
	defer cacheInvalidation.Unlock()
	cacheInvalidation.revision.Add(1)
	for _, invalidate := range cacheInvalidation.listeners {
		invalidate(hash, keyID)
	}
}
func CacheRevision() uint64 { return cacheInvalidation.revision.Load() }
func WithCacheRevision(revision uint64, store func()) bool {
	cacheInvalidation.RLock()
	defer cacheInvalidation.RUnlock()
	if revision != cacheInvalidation.revision.Load() {
		return false
	}
	store()
	return true
}

func CacheTTL(surfaces []string, ordinary time.Duration) time.Duration {
	if surfaces != nil && ordinary > RestrictedCacheTTL {
		return RestrictedCacheTTL
	}
	return ordinary
}
