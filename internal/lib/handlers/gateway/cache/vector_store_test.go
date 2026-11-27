package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Requires a real Redis: EVS_TEST_REDIS_ADDR=127.0.0.1:6379 (skipped otherwise;
// no in-process Redis fake is vendored). Only HSET/EXPIRE are exercised, so
// plain Redis without RediSearch is enough.
func TestRedisVectorStoreStoreSetsKeyExpiry(t *testing.T) {
	addr := os.Getenv("EVS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("EVS_TEST_REDIS_ADDR not set")
	}
	rc := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rc.Close() })
	ctx := context.Background()
	if err := rc.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}

	vs := &RedisVectorStore{client: &RedisClient{client: rc}, indexName: "test_idx", dimension: 2, ttl: 90 * time.Second}
	partition := SemanticPartition("tenant-a", []byte("ctx"))
	key := "cache:semantic:tenant-a:" + partition + ":q"
	t.Cleanup(func() { rc.Del(ctx, key) })

	if err := vs.Store(ctx, key, []float32{0.1, 0.2}, &CachedResponse{Response: []byte("r")}, partition); err != nil {
		t.Fatal(err)
	}
	ttl, err := rc.TTL(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > 90*time.Second {
		t.Fatalf("vector entry has no bounded expiry: TTL=%v", ttl)
	}
	if got, _ := rc.HGet(ctx, key, "tenant_id").Result(); got != partition {
		t.Fatalf("stored partition tag %q, want %q", got, partition)
	}
}
