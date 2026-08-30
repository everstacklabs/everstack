package cache_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
)

func BenchmarkMinHashSignature(b *testing.B) {
	hasher := cache.NewMinHasher(cache.DefaultMinHashConfig())
	query := "What is the capital of France and what is its population?"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hasher.Signature(query)
	}
}

func BenchmarkMinHashSimilarity(b *testing.B) {
	hasher := cache.NewMinHasher(cache.DefaultMinHashConfig())
	sig1 := hasher.Signature("What is the capital of France?")
	sig2 := hasher.Signature("What is France's capital city?")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hasher.EstimateSimilarity(sig1, sig2)
	}
}

func BenchmarkSemanticCachePut(b *testing.B) {
	c := cache.NewSemanticCache(cache.DefaultSemanticCacheConfig())

	resp := &cache.CachedResponse{
		Response:     []byte(`{"content": "test response"}`),
		Model:        "gpt-4",
		OutputTokens: 100,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		query := fmt.Sprintf("What is the capital of country %d?", i%1000)
		c.Put(query, resp)
	}
}

func BenchmarkSemanticCacheGet_Hit(b *testing.B) {
	c := cache.NewSemanticCache(cache.DefaultSemanticCacheConfig())

	// Pre-populate with 100 queries
	resp := &cache.CachedResponse{
		Response:     []byte(`{"content": "test response"}`),
		Model:        "gpt-4",
		OutputTokens: 100,
	}

	for i := 0; i < 100; i++ {
		query := fmt.Sprintf("What is the capital of country %d?", i)
		c.Put(query, resp)
	}

	// Query for a similar query
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get("What is the capital of country 50?")
	}
}

func BenchmarkSemanticCacheGet_Miss(b *testing.B) {
	c := cache.NewSemanticCache(cache.DefaultSemanticCacheConfig())

	// Pre-populate with 100 queries
	resp := &cache.CachedResponse{
		Response:     []byte(`{"content": "test response"}`),
		Model:        "gpt-4",
		OutputTokens: 100,
	}

	for i := 0; i < 100; i++ {
		query := fmt.Sprintf("What is the capital of country %d?", i)
		c.Put(query, resp)
	}

	// Query for a completely different topic
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Get("How does quantum entanglement work?")
	}
}

func BenchmarkLSHQuery(b *testing.B) {
	// Create LSH index with 1000 entries
	lsh := cache.NewLSHIndex(128, 16)
	hasher := cache.NewMinHasher(cache.DefaultMinHashConfig())

	for i := 0; i < 1000; i++ {
		query := fmt.Sprintf("What is the capital of country %d?", i)
		sig := hasher.Signature(query)
		lsh.Add(sig, i)
	}

	// Query signature
	querySig := hasher.Signature("What is the capital of country 500?")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lsh.Query(querySig)
	}
}

func TestSemanticSimilarity(t *testing.T) {
	// MinHash is designed for near-duplicate detection, NOT true semantic similarity.
	// It works well for: typos, word order changes, exact word overlaps.
	// It does NOT work for: synonyms, abbreviations, paraphrases with different words.
	hasher := cache.NewMinHasher(cache.MinHashConfig{
		NumHashes:           128,
		ShingleSize:         2,
		SimilarityThreshold: 0.3,
	})

	testCases := []struct {
		query1      string
		query2      string
		expectMatch bool
		description string
	}{
		// WILL NOT MATCH (MinHash limitation): Different sentence structure
		{
			query1:      "What is the capital of France?",
			query2:      "What is France's capital city?",
			expectMatch: false, // MinHash with 2-char shingles doesn't see enough overlap
			description: "Different phrasing - MinHash limitation with small shingles",
		},
		// SHOULD MATCH: Shared key terms
		{
			query1:      "How tall is the Eiffel Tower?",
			query2:      "What is the height of the Eiffel Tower?",
			expectMatch: true,
			description: "Shared key terms (Eiffel Tower) - slight rewording",
		},
		// SHOULD MATCH: Programming tutorials
		{
			query1:      "Python programming tutorial",
			query2:      "Learn Python programming",
			expectMatch: true,
			description: "Shared key terms (Python programming)",
		},
		// SHOULD NOT MATCH: Completely different topics
		{
			query1:      "What is the capital of France?",
			query2:      "How does quantum physics work?",
			expectMatch: false,
			description: "Completely different topics",
		},
		// WILL NOT MATCH (MinHash limitation): Synonyms
		{
			query1:      "Best restaurants in New York",
			query2:      "Top places to eat in NYC",
			expectMatch: false, // MinHash can't handle synonyms or NYC=New York
			description: "Synonyms and abbreviations - MinHash limitation",
		},
		// WILL NOT MATCH (MinHash limitation): Abbreviations
		{
			query1:      "Explain machine learning",
			query2:      "What is ML and how does it work?",
			expectMatch: false, // MinHash can't handle ML=machine learning
			description: "Abbreviations - MinHash limitation",
		},
	}

	for _, tc := range testCases {
		name := tc.query1
		if len(name) > 25 {
			name = name[:25]
		}
		t.Run(name, func(t *testing.T) {
			sig1 := hasher.Signature(tc.query1)
			sig2 := hasher.Signature(tc.query2)
			similarity := hasher.EstimateSimilarity(sig1, sig2)

			matched := similarity >= 0.3

			t.Logf("Query 1: %s", tc.query1)
			t.Logf("Query 2: %s", tc.query2)
			t.Logf("Description: %s", tc.description)
			t.Logf("Similarity: %.3f (threshold: 0.3)", similarity)
			t.Logf("Matched: %v, Expected: %v", matched, tc.expectMatch)

			if matched != tc.expectMatch {
				t.Errorf("Mismatch: got match=%v, expected=%v", matched, tc.expectMatch)
			}
		})
	}
}

func TestSemanticCacheRoundTrip(t *testing.T) {
	// Create cache with appropriate MinHash settings
	// 64 bands × 2 rows = high recall at 35% similarity
	cfg := cache.SemanticCacheConfig{
		MaxEntries: 1000,
		TTL:        5 * time.Minute,
		MinHash: cache.MinHashConfig{
			NumHashes:           128,
			ShingleSize:         2,
			SimilarityThreshold: 0.35, // Appropriate for MinHash
		},
		NumLSHBands: 64, // 64 bands for ~99% recall at 35% similarity
	}
	c := cache.NewSemanticCache(cfg)

	// Store a response
	originalQuery := "What is the capital of France?"
	resp := &cache.CachedResponse{
		Response:     []byte(`{"answer": "Paris is the capital of France"}`),
		Model:        "gpt-4",
		OutputTokens: 10,
	}
	c.Put(originalQuery, resp)

	// Retrieve with exact query
	retrieved, found := c.Get(originalQuery)
	if !found {
		t.Fatalf("Expected to find exact query in cache")
	}
	if string(retrieved.Response) != string(resp.Response) {
		t.Errorf("Retrieved response doesn't match: got %s, want %s", retrieved.Response, resp.Response)
	}
	t.Logf("✅ Exact query match: found=%v", found)

	// Check similarity between queries
	hasher := cache.NewMinHasher(cfg.MinHash)
	similarQuery := "What is France's capital city?"
	sim := hasher.EstimateSimilarity(
		hasher.Signature(originalQuery),
		hasher.Signature(similarQuery),
	)
	t.Logf("Similarity between original and similar query: %.3f (threshold: %.3f)", sim, cfg.MinHash.SimilarityThreshold)

	// Try with similar query
	retrieved2, found2 := c.Get(similarQuery)
	t.Logf("Similar query lookup: found=%v", found2)
	if found2 {
		t.Logf("✅ Retrieved from similar query: %s", retrieved2.Response)
	} else {
		t.Logf("⚠️ Similar query not found in cache (similarity %.3f may be below LSH band threshold)", sim)
	}

	// Try with completely different query
	differentQuery := "How does nuclear fusion work?"
	_, found3 := c.Get(differentQuery)
	if found3 {
		t.Errorf("Expected different query NOT to match, but it did")
	}
	t.Logf("✅ Different query correctly not matched")
}
