package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/pinecone-io/go-pinecone/v5/pinecone"
)

// pineconeMetaSentinelID is the well-known vector ID used to store collection
// metadata inside each Pinecone index namespace.
const pineconeMetaSentinelID = "00000000-0000-0000-0000-000000000000"

// PineconeStore implements VectorStore using the Pinecone managed vector database.
//
// Architecture:
//   - One serverless index per memory collection.
//   - Index name: mf-{tenant_id_short}-{collection_name} (max 45 chars, lowercase).
//   - A "_meta" namespace in each index stores a sentinel vector with collection metadata.
//   - A "data" namespace stores actual embedding vectors.
//   - Tenant isolation via index naming prefix.
type PineconeStore struct {
	mu     sync.Mutex
	client *pinecone.Client

	apiKey      string
	cloud       string // aws, gcp, azure
	region      string // e.g. us-east-1
	environment string // for pod indexes; empty for serverless
}

// PineconeConfig holds configuration for the Pinecone backend.
type PineconeConfig struct {
	APIKey      string
	Cloud       string
	Region      string
	Environment string
}

// NewPineconeStore creates a new Pinecone-backed vector store.
// The connection is established lazily on first use.
func NewPineconeStore(cfg PineconeConfig) *PineconeStore {
	return &PineconeStore{
		apiKey:      cfg.APIKey,
		cloud:       cfg.Cloud,
		region:      cfg.Region,
		environment: cfg.Environment,
	}
}

func (s *PineconeStore) Name() string { return "pinecone" }

// ensureClient lazily creates the Pinecone client.
func (s *PineconeStore) ensureClient() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return nil
	}

	client, err := pinecone.NewClient(pinecone.NewClientParams{
		ApiKey:    s.apiKey,
		SourceTag: "everstack",
	})
	if err != nil {
		return fmt.Errorf("pinecone: failed to create client: %w", err)
	}

	s.client = client
	return nil
}

// HealthCheck verifies that the Pinecone API is reachable by listing indexes.
func (s *PineconeStore) HealthCheck(ctx context.Context) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	_, err := s.client.ListIndexes(ctx)
	return err
}

// pineconeIndexName builds a Pinecone-compatible index name.
// Pinecone index names: lowercase alphanumeric + hyphens, max 45 chars.
func pineconeIndexName(tenantID, name string) string {
	// Use first 8 chars of tenant ID to keep name short.
	short := tenantID
	if len(short) > 8 {
		short = short[:8]
	}
	raw := fmt.Sprintf("mf-%s-%s", short, name)
	// Sanitize: lowercase, replace non-alphanumeric with hyphens.
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32 // lowercase
		}
		return '-'
	}, raw)
	if len(sanitized) > 45 {
		sanitized = sanitized[:45]
	}
	return sanitized
}

func toPineconeMetric(dm DistanceMetric) pinecone.IndexMetric {
	switch dm {
	case DistanceCosine:
		return pinecone.Cosine
	case DistanceEuclidean:
		return pinecone.Euclidean
	case DistanceDotProduct:
		return pinecone.Dotproduct
	default:
		return pinecone.Cosine
	}
}

func (s *PineconeStore) CreateCollection(ctx context.Context, tenantID string, opts CollectionOptions) (*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	indexName := pineconeIndexName(tenantID, opts.Name)
	dim := int32(opts.EmbeddingDimension)
	metric := toPineconeMetric(opts.DistanceMetric)

	cloud := pinecone.Cloud(s.cloud)
	if cloud == "" {
		cloud = pinecone.Aws
	}
	region := s.region
	if region == "" {
		region = "us-east-1"
	}

	_, err := s.client.CreateServerlessIndex(ctx, &pinecone.CreateServerlessIndexRequest{
		Name:      indexName,
		Dimension: &dim,
		Metric:    &metric,
		Cloud:     cloud,
		Region:    region,
	})
	if err != nil {
		return nil, fmt.Errorf("pinecone: failed to create index %q: %w", indexName, err)
	}

	// Wait for index to be ready (poll with backoff).
	if err := s.waitForIndexReady(ctx, indexName); err != nil {
		return nil, fmt.Errorf("pinecone: index %q not ready: %w", indexName, err)
	}

	now := time.Now()
	id := uuid.New().String()

	// Store collection metadata as a sentinel vector in the "_meta" namespace.
	if err := s.upsertSentinel(ctx, indexName, id, tenantID, opts, now); err != nil {
		// Clean up the index on failure.
		_ = s.client.DeleteIndex(ctx, indexName)
		return nil, fmt.Errorf("pinecone: failed to store metadata: %w", err)
	}

	logger.WithFields("backend", "pinecone", "index", indexName).
		Debug("pinecone: created collection")

	return &Collection{
		ID:                 id,
		TenantID:           tenantID,
		Name:               opts.Name,
		Description:        opts.Description,
		EmbeddingModel:     opts.EmbeddingModel,
		EmbeddingDimension: opts.EmbeddingDimension,
		DistanceMetric:     opts.DistanceMetric,
		DocumentCount:      0,
		Metadata:           opts.Metadata,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

func (s *PineconeStore) GetCollection(ctx context.Context, tenantID, name string) (*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	indexName := pineconeIndexName(tenantID, name)

	idx, err := s.client.DescribeIndex(ctx, indexName)
	if err != nil {
		return nil, fmt.Errorf("collection %q not found: %w", name, err)
	}

	return s.readSentinel(ctx, idx)
}

func (s *PineconeStore) ListCollections(ctx context.Context, tenantID string) ([]*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	indexes, err := s.client.ListIndexes(ctx)
	if err != nil {
		return nil, fmt.Errorf("pinecone: failed to list indexes: %w", err)
	}

	prefix := fmt.Sprintf("mf-%s-", tenantID[:min(8, len(tenantID))])
	var result []*Collection

	for _, idx := range indexes {
		if !strings.HasPrefix(idx.Name, prefix) {
			continue
		}
		coll, err := s.readSentinel(ctx, idx)
		if err != nil {
			logger.WithFields("index", idx.Name, "error", err.Error()).
				Warn("pinecone: failed to read sentinel, skipping")
			continue
		}
		result = append(result, coll)
	}

	return result, nil
}

func (s *PineconeStore) DeleteCollection(ctx context.Context, tenantID, name string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	indexName := pineconeIndexName(tenantID, name)
	if err := s.client.DeleteIndex(ctx, indexName); err != nil {
		return fmt.Errorf("pinecone: failed to delete index %q: %w", indexName, err)
	}

	logger.WithFields("backend", "pinecone", "index", indexName).
		Debug("pinecone: deleted collection")
	return nil
}

func (s *PineconeStore) AddDocuments(ctx context.Context, collectionID string, docs []Document) ([]string, error) {
	// collectionID is the Pinecone index name.
	// Documents are tracked by metadata on embedding vectors; just generate IDs.
	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = uuid.New().String()
	}
	return ids, nil
}

func (s *PineconeStore) DeleteDocument(ctx context.Context, tenantID, documentID string) error {
	return fmt.Errorf("pinecone: DeleteDocument requires collection context — use the memory tools which provide collection info")
}

// DeleteDocumentFromCollection removes all vectors matching a document_id from a Pinecone index.
func (s *PineconeStore) DeleteDocumentFromCollection(ctx context.Context, indexName, documentID string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	idx, err := s.client.DescribeIndex(ctx, indexName)
	if err != nil {
		return fmt.Errorf("pinecone: index %q not found: %w", indexName, err)
	}

	idxConn, err := s.client.Index(pinecone.NewIndexConnParams{
		Host:      idx.Host,
		Namespace: "data",
	})
	if err != nil {
		return fmt.Errorf("pinecone: failed to connect to index: %w", err)
	}
	defer idxConn.Close()

	filter, err := pinecone.NewMetadataFilter(map[string]interface{}{
		"document_id": map[string]interface{}{
			"$eq": documentID,
		},
	})
	if err != nil {
		return fmt.Errorf("pinecone: failed to create filter: %w", err)
	}

	if err := idxConn.DeleteVectorsByFilter(ctx, filter); err != nil {
		return fmt.Errorf("pinecone: failed to delete vectors for document %q: %w", documentID, err)
	}

	return nil
}

func (s *PineconeStore) Store(ctx context.Context, collectionID string, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	if err := s.ensureClient(); err != nil {
		return err
	}

	idx, err := s.client.DescribeIndex(ctx, collectionID)
	if err != nil {
		return fmt.Errorf("pinecone: index %q not found: %w", collectionID, err)
	}

	idxConn, err := s.client.Index(pinecone.NewIndexConnParams{
		Host:      idx.Host,
		Namespace: "data",
	})
	if err != nil {
		return fmt.Errorf("pinecone: failed to connect to index: %w", err)
	}
	defer idxConn.Close()

	vectors := make([]*pinecone.Vector, len(chunks))
	for i, chunk := range chunks {
		metadataJSON, _ := json.Marshal(chunk.Metadata)
		metadata, _ := pinecone.NewMetadata(map[string]interface{}{
			"document_id": chunk.DocumentID,
			"chunk_text":  chunk.Text,
			"chunk_index": chunk.ChunkIndex,
			"metadata":    string(metadataJSON),
		})

		vals := make([]float32, len(chunk.Embedding))
		copy(vals, chunk.Embedding)
		vectors[i] = &pinecone.Vector{
			Id:       uuid.New().String(),
			Values:   &vals,
			Metadata: metadata,
		}
	}

	_, err = idxConn.UpsertVectors(ctx, vectors)
	if err != nil {
		return fmt.Errorf("pinecone: failed to upsert %d chunks: %w", len(chunks), err)
	}

	logger.WithFields("index", collectionID, "chunks", len(chunks)).
		Debug("pinecone: stored embedding chunks")
	return nil
}

func (s *PineconeStore) Query(ctx context.Context, collectionID string, embedding []float32, opts QueryOptions) ([]SearchResult, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	idx, err := s.client.DescribeIndex(ctx, collectionID)
	if err != nil {
		return nil, fmt.Errorf("pinecone: index %q not found: %w", collectionID, err)
	}

	idxConn, err := s.client.Index(pinecone.NewIndexConnParams{
		Host:      idx.Host,
		Namespace: "data",
	})
	if err != nil {
		return nil, fmt.Errorf("pinecone: failed to connect to index: %w", err)
	}
	defer idxConn.Close()

	topK := uint32(opts.TopK)
	if topK == 0 {
		topK = 10
	}

	resp, err := idxConn.QueryByVectorValues(ctx, &pinecone.QueryByVectorValuesRequest{
		Vector:          embedding,
		TopK:            topK,
		IncludeMetadata: true,
	})
	if err != nil {
		return nil, fmt.Errorf("pinecone: query failed: %w", err)
	}

	var results []SearchResult
	for _, match := range resp.Matches {
		if opts.MinScore > 0 && match.Score < opts.MinScore {
			continue
		}

		sr := SearchResult{
			Score: match.Score,
		}

		if match.Vector != nil && match.Vector.Metadata != nil {
			fields := match.Vector.Metadata.AsMap()
			if v, ok := fields["document_id"].(string); ok {
				sr.DocumentID = v
			}
			if v, ok := fields["chunk_text"].(string); ok {
				sr.ChunkText = v
			}
			if v, ok := fields["chunk_index"].(float64); ok {
				sr.ChunkIndex = int(v)
			}
			if v, ok := fields["metadata"].(string); ok {
				var meta map[string]string
				if err := json.Unmarshal([]byte(v), &meta); err == nil {
					sr.Metadata = meta
				}
			}
		}

		results = append(results, sr)
	}

	return results, nil
}

// --- Internal helpers ---

// waitForIndexReady polls until the index is in Ready state.
func (s *PineconeStore) waitForIndexReady(ctx context.Context, indexName string) error {
	for i := 0; i < 60; i++ {
		idx, err := s.client.DescribeIndex(ctx, indexName)
		if err == nil && idx.Status != nil && idx.Status.Ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for index to be ready")
}

// upsertSentinel stores collection metadata as a sentinel vector.
func (s *PineconeStore) upsertSentinel(ctx context.Context, indexName, id, tenantID string, opts CollectionOptions, now time.Time) error {
	idx, err := s.client.DescribeIndex(ctx, indexName)
	if err != nil {
		return err
	}

	idxConn, err := s.client.Index(pinecone.NewIndexConnParams{
		Host:      idx.Host,
		Namespace: "_meta",
	})
	if err != nil {
		return err
	}
	defer idxConn.Close()

	metadataJSON, _ := json.Marshal(opts.Metadata)
	metadata, err := pinecone.NewMetadata(map[string]interface{}{
		"_type":               "collection_meta",
		"_id":                 id,
		"name":                opts.Name,
		"tenant_id":           tenantID,
		"description":         opts.Description,
		"embedding_model":     opts.EmbeddingModel,
		"embedding_dimension": opts.EmbeddingDimension,
		"distance_metric":     string(opts.DistanceMetric),
		"document_count":      0,
		"metadata":            string(metadataJSON),
		"created_at":          now.Format(time.RFC3339),
		"updated_at":          now.Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("failed to create metadata: %w", err)
	}

	// Zero vector matching index dimension.
	zeroVec := make([]float32, opts.EmbeddingDimension)

	_, err = idxConn.UpsertVectors(ctx, []*pinecone.Vector{
		{
			Id:       pineconeMetaSentinelID,
			Values:   &zeroVec,
			Metadata: metadata,
		},
	})
	return err
}

// readSentinel reads collection metadata from the sentinel vector.
func (s *PineconeStore) readSentinel(ctx context.Context, idx *pinecone.Index) (*Collection, error) {
	idxConn, err := s.client.Index(pinecone.NewIndexConnParams{
		Host:      idx.Host,
		Namespace: "_meta",
	})
	if err != nil {
		return nil, fmt.Errorf("pinecone: failed to connect: %w", err)
	}
	defer idxConn.Close()

	resp, err := idxConn.FetchVectors(ctx, []string{pineconeMetaSentinelID})
	if err != nil {
		return nil, fmt.Errorf("pinecone: failed to fetch sentinel: %w", err)
	}

	vec, ok := resp.Vectors[pineconeMetaSentinelID]
	if !ok || vec.Metadata == nil {
		return nil, fmt.Errorf("pinecone: no sentinel metadata found in index %q", idx.Name)
	}

	fields := vec.Metadata.AsMap()
	coll := &Collection{
		ID:             getString(fields, "_id"),
		TenantID:       getString(fields, "tenant_id"),
		Name:           getString(fields, "name"),
		Description:    getString(fields, "description"),
		EmbeddingModel: getString(fields, "embedding_model"),
		DistanceMetric: DistanceMetric(getString(fields, "distance_metric")),
	}

	if v, ok := fields["embedding_dimension"].(float64); ok {
		coll.EmbeddingDimension = int(v)
	}
	if v, ok := fields["document_count"].(float64); ok {
		coll.DocumentCount = int(v)
	}
	if v, ok := fields["metadata"].(string); ok {
		var meta map[string]string
		if err := json.Unmarshal([]byte(v), &meta); err == nil {
			coll.Metadata = meta
		}
	}
	if v, ok := fields["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			coll.CreatedAt = t
		}
	}
	if v, ok := fields["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			coll.UpdatedAt = t
		}
	}

	return coll, nil
}

// getString extracts a string from a map[string]interface{}.
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
