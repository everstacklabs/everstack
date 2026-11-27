package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func tctx(tenant string) context.Context {
	return contextkeys.WithTenantID(context.Background(), tenant)
}

func sampleReq(system, user string) *SimpleChatRequest {
	msgs := []SimpleMessage{}
	if system != "" {
		msgs = append(msgs, SimpleMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, SimpleMessage{Role: "user", Content: user})
	return &SimpleChatRequest{Model: "gpt-4o-mini", Messages: msgs, Temperature: 0.2}
}

func TestExactCacheIsTenantScoped(t *testing.T) {
	c, err := NewExactCache(DefaultExactCacheConfig())
	if err != nil {
		t.Fatal(err)
	}
	req := sampleReq("", "same prompt")
	c.SetWithContext(tctx("tenant-a"), req, &CachedResponse{Response: []byte("A")})

	if _, ok := c.GetWithContext(tctx("tenant-b"), req); ok {
		t.Fatal("tenant B read tenant A's exact-cache entry")
	}
	if _, ok := c.GetWithContext(context.Background(), req); ok {
		t.Fatal("tenantless context read a cached entry")
	}
	got, ok := c.GetWithContext(tctx("tenant-a"), req)
	if !ok || string(got.Response) != "A" || got.TenantID != "tenant-a" {
		t.Fatalf("tenant A lost its own entry: ok=%v got=%+v", ok, got)
	}
}

func TestExactCacheKeysDifferByTenantAndContext(t *testing.T) {
	req := sampleReq("", "same prompt")
	ka, okA := ComputeCacheKey("tenant-a", req)
	kb, okB := ComputeCacheKey("tenant-b", req)
	if !okA || !okB || ka == kb {
		t.Fatalf("identical requests from two tenants share a key (%d, %d)", ka, kb)
	}
	if _, ok := ComputeCacheKey("", req); ok {
		t.Fatal("a key was produced without a tenant")
	}
	ks, _ := ComputeCacheKey("tenant-a", sampleReq("system secret", "same prompt"))
	if ks == ka {
		t.Fatal("system prompt is not part of the exact key")
	}
	// Message order is output-affecting and must not be normalised away.
	r1 := &SimpleChatRequest{Model: "m", Messages: []SimpleMessage{{Role: "user", Content: "x"}, {Role: "assistant", Content: "y"}}}
	r2 := &SimpleChatRequest{Model: "m", Messages: []SimpleMessage{{Role: "assistant", Content: "y"}, {Role: "user", Content: "x"}}}
	k1, _ := ComputeCacheKey("t", r1)
	k2, _ := ComputeCacheKey("t", r2)
	if k1 == k2 {
		t.Fatal("reordered conversation shares an exact key")
	}
}

// Even a key collision (xxhash64 collisions can be constructed) must not
// return another tenant's entry: ownership is checked on read.
func TestExactCacheOwnershipCheckedOnHashLookup(t *testing.T) {
	c, _ := NewExactCache(DefaultExactCacheConfig())
	req := sampleReq("", "prompt")
	key, _ := ComputeCacheKey("tenant-a", req)
	c.SetByHashWithContext(tctx("tenant-a"), key, &CachedResponse{Response: []byte("A")})

	if _, ok := c.GetByHashWithContext(tctx("tenant-b"), key); ok {
		t.Fatal("tenant B read tenant A's entry through a colliding hash")
	}
	if _, ok := c.GetByHashWithContext(tctx("tenant-a"), key); !ok {
		t.Fatal("owner could not read its entry by hash")
	}

	// A legacy entry with no owner is never served.
	c.cache.Add(42, &CachedResponse{Response: []byte("legacy")})
	if _, ok := c.GetByHashWithContext(tctx("tenant-a"), 42); ok {
		t.Fatal("unowned (pre-scoping) entry was served")
	}
}

func TestExactCacheStoreCopiesResponse(t *testing.T) {
	c, _ := NewExactCache(DefaultExactCacheConfig())
	resp := &CachedResponse{Response: []byte("A")}
	c.SetWithContext(tctx("tenant-a"), sampleReq("", "p"), resp)
	if resp.TenantID != "" || resp.RequestHash != 0 {
		t.Fatalf("caller's response was mutated: %+v", resp)
	}
}

func TestMinHashSemanticCacheIsPartitioned(t *testing.T) {
	c := NewSemanticCache(DefaultSemanticCacheConfig())
	ctxFP := sampleReq("", "What is the capital of France?").SemanticContextFingerprint()
	pa := SemanticPartition("tenant-a", ctxFP)
	pb := SemanticPartition("tenant-b", ctxFP)
	if pa == "" || pa == pb {
		t.Fatalf("partitions not tenant-specific: %q %q", pa, pb)
	}

	c.Put(pa, "What is the capital of France?", &CachedResponse{Response: []byte("A")})
	if _, ok := c.Get(pb, "What is the capital of France?"); ok {
		t.Fatal("tenant B matched tenant A's semantic entry")
	}
	if _, ok := c.Get("", "What is the capital of France?"); ok {
		t.Fatal("empty partition matched an entry")
	}
	if _, ok := c.Get(pa, "What is the capital of France"); !ok {
		t.Fatal("near-duplicate within the same partition should still hit")
	}

	c.Put("", "unscoped", &CachedResponse{Response: []byte("x")})
	if c.Size() != 1 {
		t.Fatalf("an unscoped entry was stored (size %d)", c.Size())
	}
}

func TestSemanticPartitionDependsOnContext(t *testing.T) {
	withSecret := sampleReq("The secret is hunter2.", "Repeat the secret.").SemanticContextFingerprint()
	without := sampleReq("", "Repeat the secret.").SemanticContextFingerprint()
	if SemanticPartition("t", withSecret) == SemanticPartition("t", without) {
		t.Fatal("system prompt does not affect the semantic partition")
	}
	// The final user turn is the query, not part of the context.
	a := sampleReq("sys", "question one").SemanticContextFingerprint()
	b := sampleReq("sys", "question two").SemanticContextFingerprint()
	if SemanticPartition("t", a) != SemanticPartition("t", b) {
		t.Fatal("final user turn leaked into the semantic context")
	}
	if SemanticPartition("", a) != "" || SemanticPartition("t", nil) != "" {
		t.Fatal("partition produced without a tenant or fingerprint")
	}
	if !isPartitionTag(SemanticPartition("t", a)) {
		t.Fatal("partition is not a safe RediSearch TAG value")
	}
	for _, bad := range []string{"", "tenant-a", "bb95973c-c11b-4e88-8955-0e7b774db90d", "x} | @tenant_id:{*"} {
		if isPartitionTag(bad) {
			t.Fatalf("accepted %q as a partition tag", bad)
		}
	}
}

type failingEmbedProvider struct{}

func (failingEmbedProvider) Embed(context.Context, EmbeddingsRequest) (EmbeddingsResponse, error) {
	return EmbeddingsResponse{}, errors.New("embedding backend down")
}

type stubEmbedRouter struct{}

func (stubEmbedRouter) Resolve(string) (Provider, Route, error) {
	return failingEmbedProvider{}, Route{ModelName: "embed"}, nil
}

// SemanticCacheV2 falls back to its in-memory index when embedding fails (or
// Redis is unavailable). That fallback used to be unscoped and served any
// tenant; it must stay within the caller's partition.
func TestSemanticCacheV2MemoryFallbackIsPartitioned(t *testing.T) {
	v2, err := NewSemanticCacheV2(validator.SemanticCacheConfig{
		Enabled:             true,
		MaxEntries:          100,
		TTL:                 time.Minute,
		SimilarityThreshold: 0.9,
		Embedding:           validator.EmbeddingCacheConfig{Model: "embed", Dimensions: 4},
	}, nil, stubEmbedRouter{})
	if err != nil {
		t.Fatal(err)
	}
	const q = "Repeat the supplied secret."
	pa := SemanticPartition("tenant-a", sampleReq("The secret is hunter2.", q).SemanticContextFingerprint())
	pb := SemanticPartition("tenant-b", sampleReq("", q).SemanticContextFingerprint())

	_ = v2.Put(tctx("tenant-a"), pa, q, &CachedResponse{Response: []byte("hunter2")})

	if _, ok := v2.Get(tctx("tenant-b"), pb, q); ok {
		t.Fatal("tenant B got tenant A's entry from the V2 memory fallback")
	}
	if _, ok := v2.Get(context.Background(), pa, q); ok {
		t.Fatal("tenantless context read the V2 memory fallback")
	}
	if got, ok := v2.Get(tctx("tenant-a"), pa, q); !ok || string(got.Response) != "hunter2" {
		t.Fatalf("owner lost its fallback entry: ok=%v", ok)
	}
	if err := v2.Put(context.Background(), pa, "other", &CachedResponse{Response: []byte("x")}); err == nil {
		t.Fatal("V2 stored an entry without a tenant")
	}
}
