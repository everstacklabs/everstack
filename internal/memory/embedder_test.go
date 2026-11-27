package memory

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// mockTestEmbedder implements EmbedderInterface for testing.
type mockTestEmbedder struct {
	dimension int
	callCount int
}

func (m *mockTestEmbedder) Embed(_ context.Context, _, text string) ([]float32, error) {
	m.callCount++
	vec := make([]float32, m.dimension)
	// Use text length as a distinguishing value
	if len(text) > 0 && m.dimension > 0 {
		vec[0] = float32(len(text))
	}
	return vec, nil
}

func (m *mockTestEmbedder) EmbedBatch(ctx context.Context, model string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := m.Embed(ctx, model, text)
		if err != nil {
			return nil, err
		}
		result[i] = vec
	}
	return result, nil
}

// errorEmbedder always returns an error.
type errorEmbedder struct{}

func (e *errorEmbedder) Embed(_ context.Context, _, _ string) ([]float32, error) {
	return nil, fmt.Errorf("embedding failed")
}

func (e *errorEmbedder) EmbedBatch(ctx context.Context, model string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := e.Embed(ctx, model, text)
		if err != nil {
			return nil, err
		}
		result[i] = vec
	}
	return result, nil
}

func TestEmbedBatch_Ordering(t *testing.T) {
	emb := &mockTestEmbedder{dimension: 4}
	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}

	results, err := emb.EmbedBatch(context.Background(), "model", texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != len(texts) {
		t.Fatalf("expected %d results, got %d", len(texts), len(results))
	}

	// Verify order is preserved: each result's first element should be the text length
	for i, text := range texts {
		expected := float32(len(text))
		if results[i][0] != expected {
			t.Fatalf("result[%d][0] = %f, expected %f (text=%q)", i, results[i][0], expected, text)
		}
	}
}

func TestEmbedBatch_Empty(t *testing.T) {
	emb := &mockTestEmbedder{dimension: 4}
	results, err := emb.EmbedBatch(context.Background(), "model", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestEmbedBatch_SingleText(t *testing.T) {
	emb := &mockTestEmbedder{dimension: 8}
	results, err := emb.EmbedBatch(context.Background(), "model", []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0]) != 8 {
		t.Fatalf("expected dimension 8, got %d", len(results[0]))
	}
}

func TestEmbedBatch_Error(t *testing.T) {
	emb := &errorEmbedder{}
	_, err := emb.EmbedBatch(context.Background(), "model", []string{"test"})
	if err == nil {
		t.Fatal("expected error from embedder")
	}
}

type testProviderSource struct {
	bundles map[string]struct {
		registry *gw.Registry
		router   *gw.Router
	}
	err error
}

func (s *testProviderSource) ProviderBundleForRequest(ctx context.Context) (*gw.Registry, *gw.Router, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	bundle, ok := s.bundles[contextkeys.GetTenantID(ctx)]
	if !ok {
		return nil, nil, nil
	}
	return bundle.registry, bundle.router, nil
}

func testEmbeddingBundle(providerName string, embed func(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error)) (*gw.Registry, *gw.Router) {
	registry := gw.NewRegistry()
	registry.Register(&gw.ProviderAdapter{
		ProviderName:       providerName,
		SupportedModelFunc: func(string) bool { return true },
		EmbedImpl:          embed,
	})
	return registry, gw.NewRouter(registry, map[string]string{"test-embedding-model": providerName})
}

func TestEmbedderUsesTenantProviderBundlePerCall(t *testing.T) {
	registryA, routerA := testEmbeddingBundle("tenant-a-provider", func(_ context.Context, _ gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
		return gw.EmbeddingsResponse{Embedding: []float64{1}}, nil
	})
	registryB, routerB := testEmbeddingBundle("tenant-b-provider", func(_ context.Context, _ gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
		return gw.EmbeddingsResponse{Embedding: []float64{2}}, nil
	})
	embedder := NewEmbedder(&testProviderSource{bundles: map[string]struct {
		registry *gw.Registry
		router   *gw.Router
	}{
		"tenant-a": {registry: registryA, router: routerA},
		"tenant-b": {registry: registryB, router: routerB},
	}})

	for tenantID, want := range map[string]float32{"tenant-a": 1, "tenant-b": 2} {
		ctx := contextkeys.WithTenantID(context.Background(), tenantID)
		got, err := embedder.Embed(ctx, "test-embedding-model", "hello")
		if err != nil {
			t.Fatalf("tenant %s: unexpected error: %v", tenantID, err)
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("tenant %s: got %v, want [%v]", tenantID, got, want)
		}
	}
}

func TestEmbedderMissingProviderBundleFailsClosed(t *testing.T) {
	registry, router := testEmbeddingBundle("provider", func(_ context.Context, _ gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
		return gw.EmbeddingsResponse{Embedding: []float64{1}}, nil
	})
	tests := []struct {
		name     string
		embedder *Embedder
	}{
		{name: "nil source", embedder: NewEmbedder(nil)},
		{name: "source error", embedder: NewEmbedder(&testProviderSource{err: fmt.Errorf("tenant missing")})},
		{name: "nil bundle", embedder: NewEmbedder(&testProviderSource{bundles: map[string]struct {
			registry *gw.Registry
			router   *gw.Router
		}{}})},
		{name: "nil registry", embedder: NewEmbedder(&testProviderSource{bundles: map[string]struct {
			registry *gw.Registry
			router   *gw.Router
		}{"tenant-a": {router: router}}})},
		{name: "nil router", embedder: NewEmbedder(&testProviderSource{bundles: map[string]struct {
			registry *gw.Registry
			router   *gw.Router
		}{"tenant-a": {registry: registry}}})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := contextkeys.WithTenantID(context.Background(), "tenant-a")
			_, err := tt.embedder.Embed(ctx, "test-embedding-model", "hello")
			if err == nil || !strings.Contains(err.Error(), errNoEmbeddingProvider.Error()) {
				t.Fatalf("got error %v, want %q", err, errNoEmbeddingProvider)
			}
		})
	}
}

func TestEmbedderManagedCallWithoutTenantFailsClosed(t *testing.T) {
	registry, router := testEmbeddingBundle("provider", func(_ context.Context, _ gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
		return gw.EmbeddingsResponse{Embedding: []float64{1}}, nil
	})
	embedder := NewEmbedder(&testProviderSource{bundles: map[string]struct {
		registry *gw.Registry
		router   *gw.Router
	}{"tenant-a": {registry: registry, router: router}}})

	_, err := embedder.Embed(context.Background(), "test-embedding-model", "hello")
	if err == nil || !strings.Contains(err.Error(), errNoEmbeddingProvider.Error()) {
		t.Fatalf("got error %v, want %q", err, errNoEmbeddingProvider)
	}
}

func TestEmbedBatchPreservesOrderAndBoundsConcurrency(t *testing.T) {
	var active int32
	var maximum int32
	_, router := testEmbeddingBundle("provider", func(_ context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
		current := atomic.AddInt32(&active, 1)
		defer atomic.AddInt32(&active, -1)
		for {
			observed := atomic.LoadInt32(&maximum)
			if current <= observed || atomic.CompareAndSwapInt32(&maximum, observed, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		return gw.EmbeddingsResponse{Embedding: []float64{float64(len(req.Input))}}, nil
	})
	embedder := NewEmbedderWithRouter(router)
	texts := make([]string, 40)
	for i := range texts {
		texts[i] = strings.Repeat("x", i+1)
	}

	results, err := embedder.EmbedBatch(context.Background(), "test-embedding-model", texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range texts {
		if len(results[i]) != 1 || results[i][0] != float32(len(texts[i])) {
			t.Fatalf("result %d = %v, want [%d]", i, results[i], len(texts[i]))
		}
	}
	if got := atomic.LoadInt32(&maximum); got > embedBatchConcurrencyLimit {
		t.Fatalf("maximum concurrency = %d, limit = %d", got, embedBatchConcurrencyLimit)
	} else if got != embedBatchConcurrencyLimit {
		t.Fatalf("maximum concurrency = %d, want %d", got, embedBatchConcurrencyLimit)
	}
}
