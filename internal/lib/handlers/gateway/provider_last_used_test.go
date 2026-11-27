package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// lastUsedRecorder counts last_used_at writes per provider.
type lastUsedRecorder struct {
	mu     sync.Mutex
	writes map[string]int
	wrote  chan struct{}
}

func newLastUsedRecorder() *lastUsedRecorder {
	return &lastUsedRecorder{writes: map[string]int{}, wrote: make(chan struct{}, 1024)}
}

func (r *lastUsedRecorder) UpdateLastUsedAt(ctx context.Context, provider string) error {
	if _, bounded := ctx.Deadline(); !bounded {
		panic("last_used_at write has no deadline")
	}
	r.mu.Lock()
	r.writes[provider]++
	r.mu.Unlock()
	r.wrote <- struct{}{}
	return nil
}

func (r *lastUsedRecorder) count(provider string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes[provider]
}

// waitWrites blocks until n writes have landed, then gives any unexpected
// extra write a moment to show up.
func (r *lastUsedRecorder) waitWrites(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-r.wrote:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d expected last_used_at writes happened", i, n)
		}
	}
	time.Sleep(50 * time.Millisecond)
}

func tenantCtx(repo *lastUsedRecorder, tenant string) context.Context {
	ctx := context.WithValue(context.Background(), contextkeys.ProviderRepo, repo)
	return context.WithValue(ctx, contextkeys.TenantIDKey, tenant)
}

// One write per tenant and provider per interval, however many requests
// arrive: this used to be one UPDATE of the same row per request.
func TestProviderLastUsedIsWrittenOncePerInterval(t *testing.T) {
	repo := newLastUsedRecorder()
	ctx := tenantCtx(repo, "tenant-once")

	for i := 0; i < 500; i++ {
		touchProviderLastUsed(ctx, "provider-once")
	}
	repo.waitWrites(t, 1)
	if got := repo.count("provider-once"); got != 1 {
		t.Fatalf("500 requests produced %d last_used_at writes, want 1", got)
	}
}

// The throttle is per tenant and per provider: one tenant's traffic must not
// suppress another tenant's record, nor one provider's another's.
func TestProviderLastUsedThrottleIsPerTenantAndProvider(t *testing.T) {
	repo := newLastUsedRecorder()

	touchProviderLastUsed(tenantCtx(repo, "tenant-a"), "provider-shared")
	touchProviderLastUsed(tenantCtx(repo, "tenant-b"), "provider-shared")
	touchProviderLastUsed(tenantCtx(repo, "tenant-a"), "provider-other")
	repo.waitWrites(t, 3)

	if got := repo.count("provider-shared"); got != 2 {
		t.Fatalf("two tenants using one provider produced %d writes, want 2", got)
	}
	if got := repo.count("provider-other"); got != 1 {
		t.Fatalf("a second provider produced %d writes, want 1", got)
	}
}

// Once the interval has passed the next request writes again.
func TestProviderLastUsedIsWrittenAgainAfterInterval(t *testing.T) {
	repo := newLastUsedRecorder()
	ctx := tenantCtx(repo, "tenant-again")

	touchProviderLastUsed(ctx, "provider-again")
	repo.waitWrites(t, 1)

	// Age the recorded touch past the interval.
	key := "tenant-again\x00provider-again"
	if _, ok := providerLastUsedTouches.Load(key); !ok {
		t.Fatalf("no throttle entry under %q", key)
	}
	providerLastUsedTouches.Store(key, time.Now().Add(-2*providerLastUsedInterval))

	touchProviderLastUsed(ctx, "provider-again")
	repo.waitWrites(t, 1)
	if got := repo.count("provider-again"); got != 2 {
		t.Fatalf("writes after the interval = %d, want 2", got)
	}
}

// Without a repository in the context there is nothing to record.
func TestProviderLastUsedWithoutRepoIsNoop(t *testing.T) {
	touchProviderLastUsed(context.Background(), "provider-none")
	if _, ok := providerLastUsedTouches.Load("\x00provider-none"); ok {
		t.Fatalf("a throttle entry was recorded with no repository to write to")
	}
}
