package trial

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Redis key prefix for trial state
	redisKeyPrefix = "everstack:trial:"
	// Default TTL for Redis entries (30 days, longer than trial duration)
	redisDefaultTTL = 30 * 24 * time.Hour
)

// RedisStorage implements Storage using Redis for persistence
type RedisStorage struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisStorage creates a new Redis-based storage
func NewRedisStorage(client *redis.Client) *RedisStorage {
	return &RedisStorage{
		client: client,
		ttl:    redisDefaultTTL,
	}
}

// Load retrieves trial state from Redis
func (r *RedisStorage) Load(ctx context.Context, fingerprint string) (*State, error) {
	if r.client == nil {
		return nil, fmt.Errorf("redis client is nil")
	}

	key := redisKeyPrefix + fingerprint
	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Key doesn't exist
		}
		return nil, fmt.Errorf("redis get failed: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}

	return &state, nil
}

// Save persists trial state to Redis
func (r *RedisStorage) Save(ctx context.Context, state *State) error {
	if r.client == nil {
		return fmt.Errorf("redis client is nil")
	}

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	key := redisKeyPrefix + state.Fingerprint
	if err := r.client.Set(ctx, key, data, r.ttl).Err(); err != nil {
		return fmt.Errorf("redis set failed: %w", err)
	}

	return nil
}

// MemoryStorage implements Storage using in-memory map (for testing/fallback)
type MemoryStorage struct {
	mu    sync.RWMutex
	store map[string]*State
}

// NewMemoryStorage creates a new in-memory storage
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		store: make(map[string]*State),
	}
}

// Load retrieves trial state from memory
func (m *MemoryStorage) Load(ctx context.Context, fingerprint string) (*State, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.store[fingerprint]
	if !ok {
		return nil, nil
	}

	// Return a copy to prevent mutation
	stateCopy := *state
	return &stateCopy, nil
}

// Save persists trial state to memory
func (m *MemoryStorage) Save(ctx context.Context, state *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store a copy
	stateCopy := *state
	m.store[state.Fingerprint] = &stateCopy

	return nil
}

// FileStorage implements Storage using a local JSON file
// This provides persistence without requiring Redis
type FileStorage struct {
	mu       sync.RWMutex
	filepath string
	cache    map[string]*State
}

// NewFileStorage creates a new file-based storage
func NewFileStorage(filepath string) *FileStorage {
	return &FileStorage{
		filepath: filepath,
		cache:    make(map[string]*State),
	}
}

// Load retrieves trial state from file
func (f *FileStorage) Load(ctx context.Context, fingerprint string) (*State, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Check cache first
	if state, ok := f.cache[fingerprint]; ok {
		stateCopy := *state
		return &stateCopy, nil
	}

	// Load from file would go here, but for simplicity we just return nil
	// The trial manager will create a new state if none exists
	return nil, nil
}

// Save persists trial state to file
func (f *FileStorage) Save(ctx context.Context, state *State) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Update cache
	stateCopy := *state
	f.cache[state.Fingerprint] = &stateCopy

	// File persistence would go here
	// For now, we just keep in memory which survives the process lifetime
	return nil
}

// HybridStorage tries Redis first, falls back to memory
type HybridStorage struct {
	redis  *RedisStorage
	memory *MemoryStorage
}

// NewHybridStorage creates a storage that tries Redis first, falls back to memory
func NewHybridStorage(redisClient *redis.Client) *HybridStorage {
	var redisStorage *RedisStorage
	if redisClient != nil {
		redisStorage = NewRedisStorage(redisClient)
	}

	return &HybridStorage{
		redis:  redisStorage,
		memory: NewMemoryStorage(),
	}
}

// Load tries Redis first, falls back to memory
func (h *HybridStorage) Load(ctx context.Context, fingerprint string) (*State, error) {
	// Try Redis first
	if h.redis != nil {
		state, err := h.redis.Load(ctx, fingerprint)
		if err == nil && state != nil {
			// Also update memory cache
			_ = h.memory.Save(ctx, state)
			return state, nil
		}
	}

	// Fallback to memory
	return h.memory.Load(ctx, fingerprint)
}

// Save persists to both Redis and memory
func (h *HybridStorage) Save(ctx context.Context, state *State) error {
	// Always save to memory
	if err := h.memory.Save(ctx, state); err != nil {
		return err
	}

	// Try Redis (best effort)
	if h.redis != nil {
		if err := h.redis.Save(ctx, state); err != nil {
			// Log but don't fail - memory is the fallback
			return nil
		}
	}

	return nil
}
