//go:build !cgo || !onnx
// +build !cgo !onnx

// Package cache provides caching implementations for the gateway.
// This file provides stub implementations when ONNX runtime is not available.
package cache

import (
	"context"
	"errors"
	"time"
)

// ONNXCache provides semantic caching using ONNX embeddings.
// This is a stub implementation when ONNX is not available.
type ONNXCache struct{}

// ONNXCacheEntry holds a cached response with its embedding.
type ONNXCacheEntry struct{}

// ONNXCacheConfig configures the ONNX cache.
type ONNXCacheConfig struct {
	ModelPath           string
	MaxEntries          int
	TTL                 time.Duration
	SimilarityThreshold float32
	EmbeddingDim        int
}

// DefaultONNXCacheConfig returns sensible defaults for ONNX caching.
func DefaultONNXCacheConfig() ONNXCacheConfig {
	return ONNXCacheConfig{}
}

// NewONNXCache returns an error when ONNX is not available.
func NewONNXCache(cfg ONNXCacheConfig) (*ONNXCache, error) {
	return nil, errors.New("ONNX runtime not available: build with -tags 'cgo onnx' to enable")
}

// Get is a no-op stub.
func (c *ONNXCache) Get(query string) (*CachedResponse, bool) {
	return nil, false
}

// GetWithContext is a no-op stub.
func (c *ONNXCache) GetWithContext(ctx context.Context, query string) (*CachedResponse, bool) {
	return nil, false
}

// Put is a no-op stub.
func (c *ONNXCache) Put(query string, response *CachedResponse) {}

// PutWithContext is a no-op stub.
func (c *ONNXCache) PutWithContext(ctx context.Context, query string, response *CachedResponse) {}

// Cleanup is a no-op stub.
func (c *ONNXCache) Cleanup() {}

// Stats returns empty statistics.
func (c *ONNXCache) Stats() map[string]interface{} {
	return map[string]interface{}{
		"available": false,
		"reason":    "ONNX runtime not compiled in",
	}
}

// Close is a no-op stub.
func (c *ONNXCache) Close() error {
	return nil
}

