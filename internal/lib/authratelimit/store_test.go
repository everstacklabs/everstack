package authratelimit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestMemoryStoreCountsAFixedWindow(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	rule := Rule{Limit: 2, Window: time.Minute}
	ctx := context.Background()

	for i := range 2 {
		decision, err := store.Consume(ctx, "k", rule)
		if err != nil || !decision.Allowed {
			t.Fatalf("call %d = %+v, %v; want allowed", i+1, decision, err)
		}
	}

	now = now.Add(15 * time.Second)
	decision, _ := store.Consume(ctx, "k", rule)
	if decision.Allowed || decision.RetryAfter != 45*time.Second {
		t.Fatalf("over the limit = %+v; want blocked for 45s", decision)
	}

	now = now.Add(46 * time.Second)
	if decision, _ := store.Consume(ctx, "k", rule); !decision.Allowed {
		t.Fatalf("after the window = %+v; want a fresh window", decision)
	}
	if decision, _ := store.Consume(ctx, "other", rule); !decision.Allowed {
		t.Fatalf("an unrelated key = %+v; want its own budget", decision)
	}
}

func TestMemoryStorePeeksWithoutCounting(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	ctx := context.Background()

	if w, _ := store.Peek(ctx, "k"); w.Count != 0 {
		t.Fatalf("empty peek = %+v", w)
	}
	for range 2 {
		_, _ = store.Consume(ctx, "k", Rule{Limit: 5, Window: time.Minute})
	}
	now = now.Add(20 * time.Second)
	for range 2 {
		if w, _ := store.Peek(ctx, "k"); w.Count != 2 || w.RetryAfter != 40*time.Second {
			t.Fatalf("peek = %+v; want count 2, 40s left, unchanged by peeking", w)
		}
	}
	now = now.Add(41 * time.Second)
	if w, _ := store.Peek(ctx, "k"); w.Count != 0 {
		t.Fatalf("expired peek = %+v", w)
	}
}

type failingStore struct{ calls int }

func (f *failingStore) Consume(context.Context, string, Rule) (Decision, error) {
	f.calls++
	return Decision{}, errors.New("dial tcp: connection refused")
}

func (f *failingStore) Peek(context.Context, string) (Window, error) {
	f.calls++
	return Window{}, errors.New("dial tcp: connection refused")
}

func TestFallbackStoreFailsClosedWhenRedisIsRequired(t *testing.T) {
	store := NewFallbackStore(&failingStore{}, NewMemoryStore(), true)

	_, err := store.Consume(context.Background(), "k", Rule{Limit: 1, Window: time.Minute})

	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v; want ErrUnavailable so callers refuse the request", err)
	}
}

func TestFallbackStoreCountsLocallyWhenRedisIsOptional(t *testing.T) {
	primary := &failingStore{}
	store := NewFallbackStore(primary, NewMemoryStore(), false)
	rule := Rule{Limit: 1, Window: time.Minute}
	ctx := context.Background()

	first, err := store.Consume(ctx, "k", rule)
	if err != nil || !first.Allowed {
		t.Fatalf("first = %+v, %v; want allowed by the local fallback", first, err)
	}
	second, _ := store.Consume(ctx, "k", rule)
	if second.Allowed {
		t.Fatal("the fallback must still enforce the limit, just per process")
	}
	if primary.calls != 2 {
		t.Fatalf("redis tried %d times; want every request to try it first so recovery is immediate", primary.calls)
	}
}

// Runs only against a real Redis, like the gateway cache tests:
// EVS_TEST_REDIS_ADDR=127.0.0.1:6379 go test ./internal/lib/authratelimit/
func TestRedisStoreSharesOneWindowAcrossReplicas(t *testing.T) {
	addr := os.Getenv("EVS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("EVS_TEST_REDIS_ADDR not set")
	}
	ctx := context.Background()
	podA := redis.NewClient(&redis.Options{Addr: addr})
	podB := redis.NewClient(&redis.Options{Addr: addr})
	defer podA.Close()
	defer podB.Close()
	prefix := fmt.Sprintf("evs:auth:rl:test:%d:", time.Now().UnixNano())
	defer podA.Del(ctx, prefix+"k")

	stores := []Store{NewRedisStore(podA, prefix), NewRedisStore(podB, prefix)}
	rule := Rule{Limit: 3, Window: 30 * time.Second}

	var mu sync.Mutex
	var allowed, blocked int
	var retryAfter time.Duration
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(store Store) {
			defer wg.Done()
			decision, err := store.Consume(ctx, "k", rule)
			if err != nil {
				t.Errorf("consume: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if decision.Allowed {
				allowed++
			} else {
				blocked++
				retryAfter = decision.RetryAfter
			}
		}(stores[i%2])
	}
	wg.Wait()

	if allowed != 3 || blocked != 5 {
		t.Fatalf("allowed=%d blocked=%d; want exactly the limit admitted across both replicas", allowed, blocked)
	}
	if retryAfter <= 0 || retryAfter > 30*time.Second {
		t.Fatalf("retryAfter = %s; want the time left in the window", retryAfter)
	}
	if ttl := podA.PTTL(ctx, prefix+"k").Val(); ttl <= 0 {
		t.Fatalf("key ttl = %s; the window must expire or the caller is locked out forever", ttl)
	}
	window, err := stores[1].Peek(ctx, "k")
	if err != nil || window.Count != 8 || window.RetryAfter <= 0 {
		t.Fatalf("peek = %+v, %v; want all 8 requests and the time left", window, err)
	}
	if empty, err := stores[0].Peek(ctx, "absent"); err != nil || empty.Count != 0 {
		t.Fatalf("peek of a missing key = %+v, %v", empty, err)
	}
}
