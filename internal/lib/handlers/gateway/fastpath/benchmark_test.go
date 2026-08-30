package fastpath_test

import (
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
)

func BenchmarkAuthCache(b *testing.B) {
	cache := fastpath.NewFastPathAuthCache(fastpath.AuthCacheConfig{
		ExpectedKeys:      100000,
		FalsePositiveRate: 0.001,
		TTL:               60 * time.Second,
	})
	apiKey := "test-key-12345"
	cache.MarkValid(apiKey)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.IsValid(apiKey)
	}
}

func BenchmarkRouterCache(b *testing.B) {
	cache := fastpath.NewRouterCache()

	info := &fastpath.ProviderInfo{
		Name:      "openai",
		ModelName: "gpt-4",
	}
	cache.Register("gpt-4", info)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Resolve("gpt-4")
	}
}

func BenchmarkJSONMarshal(b *testing.B) {
	data := map[string]interface{}{
		"model": "gpt-4",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = fastpath.Marshal(data)
	}
}
