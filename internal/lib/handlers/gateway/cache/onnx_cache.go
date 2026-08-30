//go:build cgo && onnx
// +build cgo,onnx

// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	ort "github.com/yalue/onnxruntime_go"
)

// ONNXCache provides semantic caching using ONNX embeddings.
// It uses cosine similarity between embeddings to find semantically similar queries.
type ONNXCache struct {
	// ONNX Runtime session
	session *ort.AdvancedSession
	mu      sync.RWMutex

	// Cache entries with embeddings
	entries []ONNXCacheEntry

	// Config
	cfg ONNXCacheConfig

	// Metrics
	hits   atomic.Uint64
	misses atomic.Uint64
}

// ONNXCacheEntry holds a cached response with its embedding.
type ONNXCacheEntry struct {
	Query     string
	Embedding []float32
	Response  *CachedResponse
	CreatedAt time.Time
}

// ONNXCacheConfig configures the ONNX cache.
type ONNXCacheConfig struct {
	// ModelPath is the path to the ONNX model file
	ModelPath string

	// MaxEntries is the maximum number of cached responses
	MaxEntries int

	// TTL is how long cached responses remain valid
	TTL time.Duration

	// SimilarityThreshold is the minimum cosine similarity to consider a match
	// Range: 0.0 to 1.0 (0.85 = 85% similar)
	SimilarityThreshold float32

	// EmbeddingDim is the dimension of the embedding vectors
	// For all-MiniLM-L6-v2: 384
	EmbeddingDim int
}

// DefaultONNXCacheConfig returns sensible defaults for ONNX caching.
func DefaultONNXCacheConfig() ONNXCacheConfig {
	return ONNXCacheConfig{
		ModelPath:           "./models/all-MiniLM-L6-v2.onnx",
		MaxEntries:          5000,
		TTL:                 5 * time.Minute,
		SimilarityThreshold: 0.85, // 85% cosine similarity
		EmbeddingDim:        384,  // all-MiniLM-L6-v2
	}
}

// NewONNXCache creates a new ONNX-based semantic cache.
func NewONNXCache(cfg ONNXCacheConfig) (*ONNXCache, error) {
	// Initialize ONNX Runtime
	ort.SetSharedLibraryPath("libonnxruntime.so")
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("failed to initialize ONNX runtime: %w", err)
	}

	// Load model
	session, err := ort.NewAdvancedSession(cfg.ModelPath,
		[]string{"input_ids", "attention_mask"},
		[]string{"last_hidden_state"},
		nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load ONNX model: %w", err)
	}

	logger.WithFields(
		"model_path", cfg.ModelPath,
		"embedding_dim", cfg.EmbeddingDim,
		"threshold", cfg.SimilarityThreshold,
	).Info("ONNX cache initialized")

	return &ONNXCache{
		session: session,
		entries: make([]ONNXCacheEntry, 0, cfg.MaxEntries),
		cfg:     cfg,
	}, nil
}

// Embed converts text to an embedding vector.
// This is a simplified implementation - in production, use a proper tokenizer.
func (c *ONNXCache) Embed(text string) ([]float32, error) {
	// TODO: Implement proper tokenization
	// For now, return a placeholder
	// In production, you would:
	// 1. Tokenize text using the model's tokenizer
	// 2. Convert tokens to input_ids and attention_mask
	// 3. Run inference
	// 4. Apply mean pooling to get sentence embedding

	return nil, fmt.Errorf("ONNX embedding not yet implemented - use MinHash for now")
}

// Get finds a similar cached response using cosine similarity.
// Legacy method without context - prefer GetWithContext for new code.
func (c *ONNXCache) Get(query string) (*CachedResponse, bool) {
	return c.GetWithContext(context.Background(), query)
}

// GetWithContext finds a similar cached response using cosine similarity with context support.
// Note: Tracing is handled at the fastpath engine layer to avoid import cycles.
func (c *ONNXCache) GetWithContext(ctx context.Context, query string) (*CachedResponse, bool) {
	_ = ctx // Context reserved for future use

	// Compute query embedding
	queryEmbed, err := c.Embed(query)
	if err != nil {
		c.misses.Add(1)
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Find most similar entry
	var bestMatch *ONNXCacheEntry
	bestSimilarity := float32(0)
	now := time.Now()

	for i := range c.entries {
		entry := &c.entries[i]

		// Check TTL
		if now.Sub(entry.CreatedAt) > c.cfg.TTL {
			continue
		}

		// Compute cosine similarity
		similarity := cosineSimilarity(queryEmbed, entry.Embedding)

		logger.WithFields(
			"query", query,
			"cached_query", entry.Query,
			"similarity", similarity,
			"threshold", c.cfg.SimilarityThreshold,
		).Debug("ONNX cache: similarity check")

		if similarity >= c.cfg.SimilarityThreshold && similarity > bestSimilarity {
			bestMatch = entry
			bestSimilarity = similarity
		}
	}

	if bestMatch == nil {
		c.misses.Add(1)
		logger.WithFields(
			"query", query,
			"best_similarity", bestSimilarity,
		).Debug("ONNX cache: MISS")
		return nil, false
	}

	c.hits.Add(1)
	logger.WithFields(
		"query", query,
		"cached_query", bestMatch.Query,
		"similarity", bestSimilarity,
	).Info("ONNX cache: HIT")

	return bestMatch.Response, true
}

// Put stores a response with its embedding.
// Legacy method without context - prefer PutWithContext for new code.
func (c *ONNXCache) Put(query string, response *CachedResponse) {
	c.PutWithContext(context.Background(), query, response)
}

// PutWithContext stores a response with its embedding with context support.
// Note: Tracing is handled at the fastpath engine layer to avoid import cycles.
func (c *ONNXCache) PutWithContext(ctx context.Context, query string, response *CachedResponse) {
	_ = ctx // Context reserved for future use

	embedding, err := c.Embed(query)
	if err != nil {
		logger.WithError(err).Warn("Failed to compute embedding for caching")
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := ONNXCacheEntry{
		Query:     query,
		Embedding: embedding,
		Response:  response,
		CreatedAt: time.Now(),
	}

	if len(c.entries) < c.cfg.MaxEntries {
		c.entries = append(c.entries, entry)
	} else {
		// Simple LRU: replace oldest entry
		c.entries[0] = entry
	}
}

// Cleanup removes expired entries.
func (c *ONNXCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	validEntries := make([]ONNXCacheEntry, 0, len(c.entries))

	for i := range c.entries {
		if now.Sub(c.entries[i].CreatedAt) <= c.cfg.TTL {
			validEntries = append(validEntries, c.entries[i])
		}
	}

	c.entries = validEntries
}

// Stats returns cache statistics.
func (c *ONNXCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses
	hitRatio := float64(0)
	if total > 0 {
		hitRatio = float64(hits) / float64(total)
	}

	return map[string]interface{}{
		"hits":        hits,
		"misses":      misses,
		"hit_ratio":   hitRatio,
		"entries":     len(c.entries),
		"max_entries": c.cfg.MaxEntries,
	}
}

// Close cleans up ONNX resources.
func (c *ONNXCache) Close() error {
	if c.session != nil {
		c.session.Destroy()
	}
	return nil
}

// cosineSimilarity computes the cosine similarity between two vectors.
// Returns a value between -1.0 and 1.0, where 1.0 means identical direction.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}
