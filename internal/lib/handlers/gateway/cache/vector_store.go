// Package cache provides caching implementations for the gateway.
package cache

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// VectorStore defines the interface for vector similarity search
type VectorStore interface {
	Store(ctx context.Context, key string, embedding []float32, metadata *CachedResponse, tenantID string) error
	Search(ctx context.Context, query []float32, topK int, threshold float64, tenantID string) ([]SearchResult, error)
	Delete(ctx context.Context, key string) error
	Clear(ctx context.Context, tenantID string) error
}

// SearchResult represents a search result with similarity score
type SearchResult struct {
	Key      string
	Query    string
	Response CachedResponse
	Score    float64 // Cosine similarity score (0-1)
	Distance float64 // Distance metric from RedisSearch
}

// RedisVectorStore implements vector similarity search using RedisSearch
type RedisVectorStore struct {
	client    *RedisClient
	indexName string
	dimension int
}

// NewRedisVectorStore creates a new Redis vector store with HNSW index
func NewRedisVectorStore(client *RedisClient, indexName string, dimension int) (*RedisVectorStore, error) {
	if client == nil {
		return nil, fmt.Errorf("redis client is required")
	}

	if indexName == "" {
		indexName = "semantic_cache_idx"
	}

	if dimension <= 0 {
		return nil, fmt.Errorf("dimension must be positive, got %d", dimension)
	}

	vs := &RedisVectorStore{
		client:    client,
		indexName: indexName,
		dimension: dimension,
	}

	// Create RedisSearch index if it doesn't exist
	if err := vs.createIndex(); err != nil {
		return nil, fmt.Errorf("failed to create index: %w", err)
	}

	logger.WithFields(
		"index_name", indexName,
		"dimension", dimension,
	).Info("Redis vector store initialized")

	return vs, nil
}

// createIndex creates the RedisSearch index with vector field
func (vs *RedisVectorStore) createIndex() error {
	ctx := context.Background()

	// FT.CREATE semantic_cache_idx
	//   ON HASH PREFIX 1 cache:semantic:
	//   SCHEMA
	//     query TEXT
	//     embedding VECTOR HNSW 6 TYPE FLOAT32 DIM <dimension> DISTANCE_METRIC COSINE
	//     response BLOB
	//     model TEXT
	//     created_at NUMERIC
	//     ttl NUMERIC

	// Build the vector field attributes
	// Format: VECTOR HNSW <num_params> TYPE FLOAT32 DIM <dim> DISTANCE_METRIC COSINE
	// Note: RedisSearch doesn't have BLOB type, we store response as TAG (not indexed) or skip it from schema
	cmd := []interface{}{
		"FT.CREATE", vs.indexName,
		"ON", "HASH",
		"PREFIX", "1", "cache:semantic:",
		"SCHEMA",
		"query", "TEXT",
		"tenant_id", "TAG",
		"embedding", "VECTOR", "HNSW", "6", "TYPE", "FLOAT32", "DIM", fmt.Sprintf("%d", vs.dimension), "DISTANCE_METRIC", "COSINE",
		"model", "TEXT",
		"created_at", "NUMERIC",
		"ttl", "NUMERIC",
	}

	logger.WithFields("command", fmt.Sprintf("%v", cmd)).Debug("Creating RedisSearch index")

	err := vs.client.Do(ctx, cmd...).Err()
	if err != nil {
		// Check if index already exists
		if strings.Contains(err.Error(), "Index already exists") {
			logger.WithFields("index_name", vs.indexName).Debug("RedisSearch index already exists")
			return nil
		}
		return fmt.Errorf("failed to create RedisSearch index: %w", err)
	}

	logger.WithFields("index_name", vs.indexName).Info("RedisSearch index created successfully")
	return nil
}

// Store stores an embedding with metadata in Redis
func (vs *RedisVectorStore) Store(ctx context.Context, key string, embedding []float32, metadata *CachedResponse, tenantID string) error {
	if len(embedding) != vs.dimension {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d", vs.dimension, len(embedding))
	}

	// Convert embedding to bytes
	embeddingBytes := float32SliceToBytes(embedding)

	// Serialize response
	responseBytes, err := serializeCachedResponse(metadata)
	if err != nil {
		return fmt.Errorf("failed to serialize response: %w", err)
	}

	// Store in Redis hash
	now := time.Now().Unix()
	ttl := now + int64(metadata.CreatedAt.Add(5*time.Minute).Sub(time.Now()).Seconds())

	fields := map[string]interface{}{
		"query":      "", // Will be set by caller if needed
		"tenant_id":  tenantID,
		"embedding":  embeddingBytes,
		"response":   responseBytes,
		"model":      metadata.Model,
		"created_at": now,
		"ttl":        ttl,
	}

	if err := vs.client.Client().HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("failed to store in Redis: %w", err)
	}

	return nil
}

// Search performs KNN vector similarity search
func (vs *RedisVectorStore) Search(ctx context.Context, query []float32, topK int, threshold float64, tenantID string) ([]SearchResult, error) {
	if len(query) != vs.dimension {
		return nil, fmt.Errorf("query dimension mismatch: expected %d, got %d", vs.dimension, len(query))
	}

	if topK <= 0 {
		topK = 5
	}

	// Convert embedding to bytes
	embeddingBytes := float32SliceToBytes(query)

	// FT.SEARCH semantic_cache_idx
	//   "@tenant_id:{tenantID}=>[KNN 5 @embedding $query_vec AS score]"
	//   PARAMS 2 query_vec <embedding_bytes>
	//   SORTBY score
	//   RETURN 5 query response model score created_at
	//   LIMIT 0 <topK>

	// Build the filter query — scope results to the requesting tenant.
	filterQuery := fmt.Sprintf("@tenant_id:{%s}=>[KNN %d @embedding $query_vec AS score]", tenantID, topK)

	cmd := []interface{}{
		"FT.SEARCH", vs.indexName,
		filterQuery,
		"PARAMS", "2", "query_vec", embeddingBytes,
		"SORTBY", "score",
		"RETURN", "5", "query", "response", "model", "score", "created_at",
		"LIMIT", "0", topK,
	}

	result, err := vs.client.Do(ctx, cmd...).Result()
	if err != nil {
		return nil, fmt.Errorf("RedisSearch query failed: %w", err)
	}

	// Parse results
	results, err := vs.parseSearchResults(result, threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w", err)
	}

	logger.WithFields(
		"num_results", len(results),
		"threshold", threshold,
		"top_k", topK,
	).Debug("Vector search completed")

	return results, nil
}

// parseSearchResults parses the RedisSearch FT.SEARCH response
func (vs *RedisVectorStore) parseSearchResults(result interface{}, threshold float64) ([]SearchResult, error) {
	// RedisSearch returns: [numResults, key1, fields1, key2, fields2, ...]
	arr, ok := result.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result format")
	}

	if len(arr) == 0 {
		return []SearchResult{}, nil
	}

	// First element is the number of results
	numResults, ok := arr[0].(int64)
	if !ok {
		return nil, fmt.Errorf("unexpected number of results format")
	}

	if numResults == 0 {
		return []SearchResult{}, nil
	}

	var results []SearchResult

	// Parse each result (key, fields pairs)
	for i := 1; i < len(arr); i += 2 {
		if i+1 >= len(arr) {
			break
		}

		key, ok := arr[i].(string)
		if !ok {
			continue
		}

		fields, ok := arr[i+1].([]interface{})
		if !ok {
			continue
		}

		// Parse fields
		fieldMap := make(map[string]interface{})
		for j := 0; j < len(fields); j += 2 {
			if j+1 >= len(fields) {
				break
			}
			fieldName, ok := fields[j].(string)
			if !ok {
				continue
			}
			fieldMap[fieldName] = fields[j+1]
		}

		// Extract score (distance from RedisSearch)
		distanceStr, ok := fieldMap["score"].(string)
		if !ok {
			continue
		}

		var distance float64
		fmt.Sscanf(distanceStr, "%f", &distance)

		// Convert distance to cosine similarity (RedisSearch returns distance, not similarity)
		// For cosine distance: similarity = 1 - distance
		similarity := 1.0 - distance

		// Apply threshold
		if similarity < threshold {
			continue
		}

		// Extract query text
		queryText := ""
		if q, ok := fieldMap["query"].(string); ok {
			queryText = q
		}

		// Extract and deserialize response
		responseBytes, ok := fieldMap["response"].(string)
		if !ok {
			continue
		}

		response, err := deserializeCachedResponse([]byte(responseBytes))
		if err != nil {
			logger.WithError(err).Warn("Failed to deserialize cached response")
			continue
		}

		results = append(results, SearchResult{
			Key:      key,
			Query:    queryText,
			Response: *response,
			Score:    similarity,
			Distance: distance,
		})
	}

	return results, nil
}

// Delete removes a vector from the store
func (vs *RedisVectorStore) Delete(ctx context.Context, key string) error {
	if err := vs.client.Client().Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete key %s: %w", key, err)
	}
	return nil
}

// Clear removes all vectors from the store for a given tenant.
// If tenantID is empty, all cache entries are cleared.
func (vs *RedisVectorStore) Clear(ctx context.Context, tenantID string) error {
	// Scope the SCAN pattern to the tenant's key prefix.
	pattern := "cache:semantic:*"
	if tenantID != "" {
		pattern = fmt.Sprintf("cache:semantic:%s:*", tenantID)
	}
	iter := vs.client.Client().Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		if err := vs.client.Client().Del(ctx, iter.Val()).Err(); err != nil {
			logger.WithError(err).Warn("Failed to delete key during clear")
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan keys: %w", err)
	}
	return nil
}

// float32SliceToBytes converts a float32 slice to bytes for Redis storage
func float32SliceToBytes(floats []float32) []byte {
	bytes := make([]byte, len(floats)*4)
	for i, f := range floats {
		bits := math.Float32bits(f)
		binary.LittleEndian.PutUint32(bytes[i*4:], bits)
	}
	return bytes
}

// bytesToFloat32Slice converts bytes back to float32 slice
func bytesToFloat32Slice(bytes []byte) []float32 {
	floats := make([]float32, len(bytes)/4)
	for i := range floats {
		bits := binary.LittleEndian.Uint32(bytes[i*4:])
		floats[i] = math.Float32frombits(bits)
	}
	return floats
}

// serializeCachedResponse serializes a CachedResponse to bytes
func serializeCachedResponse(resp *CachedResponse) ([]byte, error) {
	// Simple serialization - in production, use protobuf or msgpack
	return resp.Response, nil
}

// deserializeCachedResponse deserializes bytes to CachedResponse
func deserializeCachedResponse(data []byte) (*CachedResponse, error) {
	// Simple deserialization - in production, use protobuf or msgpack
	return &CachedResponse{
		Response:  data,
		CreatedAt: time.Now(),
	}, nil
}
