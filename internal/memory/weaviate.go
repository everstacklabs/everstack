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
	weaviate "github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/auth"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
	"github.com/weaviate/weaviate/entities/schema"
)

// WeaviateStore implements VectorStore using the Weaviate vector database.
//
// Architecture:
//   - One Weaviate class per memory collection.
//   - Class name: Mf_{sanitized_tenant}_{sanitized_name} (must start with uppercase).
//   - Properties: document_id, chunk_text, chunk_index, metadata, obj_type.
//   - BYO vectors (Vectorizer: "none") — we provide pre-computed embeddings.
//   - Sentinel object (obj_type = "collection_meta") stores collection metadata.
//   - Tenant isolation via class naming prefix.
type WeaviateStore struct {
	mu     sync.Mutex
	client *weaviate.Client

	host   string // e.g. "localhost:8080"
	scheme string // "http" or "https"
	apiKey string
}

// WeaviateConfig holds configuration for the Weaviate backend.
type WeaviateConfig struct {
	URL    string // full URL like "http://localhost:8080"
	APIKey string
}

// NewWeaviateStore creates a new Weaviate-backed vector store.
func NewWeaviateStore(cfg WeaviateConfig) *WeaviateStore {
	host := cfg.URL
	weaviateScheme := "http"

	// Parse URL into scheme + host.
	if strings.HasPrefix(host, "https://") {
		weaviateScheme = "https"
		host = strings.TrimPrefix(host, "https://")
	} else if strings.HasPrefix(host, "http://") {
		host = strings.TrimPrefix(host, "http://")
	}

	return &WeaviateStore{
		host:   host,
		scheme: weaviateScheme,
		apiKey: cfg.APIKey,
	}
}

func (s *WeaviateStore) Name() string { return "weaviate" }

// ensureClient lazily creates the Weaviate client.
func (s *WeaviateStore) ensureClient() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return nil
	}

	cfg := weaviate.Config{
		Host:   s.host,
		Scheme: s.scheme,
	}

	if s.apiKey != "" {
		cfg.AuthConfig = auth.ApiKey{Value: s.apiKey}
	}

	client, err := weaviate.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("weaviate: failed to create client: %w", err)
	}

	s.client = client
	return nil
}

// HealthCheck verifies that the Weaviate server is reachable.
func (s *WeaviateStore) HealthCheck(ctx context.Context) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	ready, err := s.client.Misc().ReadyChecker().Do(ctx)
	if err != nil {
		return fmt.Errorf("weaviate: health check failed: %w", err)
	}
	if !ready {
		return fmt.Errorf("weaviate: server not ready")
	}
	return nil
}

// weaviateClassName builds a Weaviate-compatible class name.
// Weaviate class names: must start with uppercase, alphanumeric only.
func weaviateClassName(tenantID, name string) string {
	short := tenantID
	if len(short) > 8 {
		short = short[:8]
	}
	// Sanitize: keep only alphanumeric, replace others with empty.
	sanitize := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return fmt.Sprintf("Mf%s%s", sanitize(short), sanitize(name))
}

func toWeaviateDistance(dm DistanceMetric) string {
	switch dm {
	case DistanceCosine:
		return "cosine"
	case DistanceEuclidean:
		return "l2-squared"
	case DistanceDotProduct:
		return "dot"
	default:
		return "cosine"
	}
}

func boolPtr(b bool) *bool { return &b }

func (s *WeaviateStore) CreateCollection(ctx context.Context, tenantID string, opts CollectionOptions) (*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	className := weaviateClassName(tenantID, opts.Name)

	classObj := &models.Class{
		Class:           className,
		Description:     opts.Description,
		Vectorizer:      "none", // BYO vectors
		VectorIndexType: "hnsw",
		VectorIndexConfig: map[string]interface{}{
			"distance": toWeaviateDistance(opts.DistanceMetric),
		},
		Properties: []*models.Property{
			{Name: "obj_type", DataType: schema.DataTypeText.PropString(), IndexFilterable: boolPtr(true)},
			{Name: "document_id", DataType: schema.DataTypeText.PropString(), IndexFilterable: boolPtr(true)},
			{Name: "chunk_text", DataType: schema.DataTypeText.PropString()},
			{Name: "chunk_index", DataType: schema.DataTypeInt.PropString(), IndexFilterable: boolPtr(true)},
			{Name: "obj_metadata", DataType: schema.DataTypeText.PropString()},
			// Sentinel-specific fields (also present as properties so they can be read back).
			{Name: "collection_id", DataType: schema.DataTypeText.PropString()},
			{Name: "collection_name", DataType: schema.DataTypeText.PropString()},
			{Name: "tenant_id", DataType: schema.DataTypeText.PropString()},
			{Name: "description", DataType: schema.DataTypeText.PropString()},
			{Name: "embedding_model", DataType: schema.DataTypeText.PropString()},
			{Name: "embedding_dimension", DataType: schema.DataTypeInt.PropString()},
			{Name: "distance_metric", DataType: schema.DataTypeText.PropString()},
			{Name: "document_count", DataType: schema.DataTypeInt.PropString()},
			{Name: "created_at", DataType: schema.DataTypeText.PropString()},
			{Name: "updated_at", DataType: schema.DataTypeText.PropString()},
		},
	}

	err := s.client.Schema().ClassCreator().WithClass(classObj).Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate: failed to create class %q: %w", className, err)
	}

	now := time.Now()
	id := uuid.New().String()

	// Insert sentinel metadata object.
	metadataJSON, _ := json.Marshal(opts.Metadata)
	zeroVec := make([]float32, opts.EmbeddingDimension)

	_, err = s.client.Data().Creator().
		WithClassName(className).
		WithProperties(map[string]interface{}{
			"obj_type":            "collection_meta",
			"collection_id":      id,
			"collection_name":    opts.Name,
			"tenant_id":          tenantID,
			"description":        opts.Description,
			"embedding_model":    opts.EmbeddingModel,
			"embedding_dimension": opts.EmbeddingDimension,
			"distance_metric":    string(opts.DistanceMetric),
			"document_count":     0,
			"obj_metadata":       string(metadataJSON),
			"created_at":         now.Format(time.RFC3339),
			"updated_at":         now.Format(time.RFC3339),
		}).
		WithVector(zeroVec).
		Do(ctx)
	if err != nil {
		// Clean up the class on failure.
		_ = s.client.Schema().ClassDeleter().WithClassName(className).Do(ctx)
		return nil, fmt.Errorf("weaviate: failed to insert sentinel: %w", err)
	}

	logger.WithFields("backend", "weaviate", "class", className).
		Debug("weaviate: created collection")

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

func (s *WeaviateStore) GetCollection(ctx context.Context, tenantID, name string) (*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	className := weaviateClassName(tenantID, name)

	_, err := s.client.Schema().ClassGetter().WithClassName(className).Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("collection %q not found: %w", name, err)
	}

	return s.readSentinel(ctx, className)
}

func (s *WeaviateStore) ListCollections(ctx context.Context, tenantID string) ([]*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	allSchema, err := s.client.Schema().Getter().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate: failed to list classes: %w", err)
	}

	prefix := "Mf" + func() string {
		short := tenantID
		if len(short) > 8 {
			short = short[:8]
		}
		var b strings.Builder
		for _, r := range short {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}()

	var result []*Collection
	for _, class := range allSchema.Classes {
		if !strings.HasPrefix(class.Class, prefix) {
			continue
		}
		coll, err := s.readSentinel(ctx, class.Class)
		if err != nil {
			logger.WithFields("class", class.Class, "error", err.Error()).
				Warn("weaviate: failed to read sentinel, skipping")
			continue
		}
		result = append(result, coll)
	}

	return result, nil
}

func (s *WeaviateStore) DeleteCollection(ctx context.Context, tenantID, name string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	className := weaviateClassName(tenantID, name)
	err := s.client.Schema().ClassDeleter().WithClassName(className).Do(ctx)
	if err != nil {
		return fmt.Errorf("weaviate: failed to delete class %q: %w", className, err)
	}

	logger.WithFields("backend", "weaviate", "class", className).
		Debug("weaviate: deleted collection")
	return nil
}

func (s *WeaviateStore) AddDocuments(ctx context.Context, collectionID string, docs []Document) ([]string, error) {
	// collectionID is the Weaviate class name.
	// Documents are tracked by metadata on embedding objects; just generate IDs.
	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = uuid.New().String()
	}
	return ids, nil
}

func (s *WeaviateStore) DeleteDocument(ctx context.Context, tenantID, documentID string) error {
	return fmt.Errorf("weaviate: DeleteDocument requires collection context — use the memory tools which provide collection info")
}

// DeleteDocumentFromCollection removes all objects matching a document_id from a Weaviate class.
func (s *WeaviateStore) DeleteDocumentFromCollection(ctx context.Context, className, documentID string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	where := filters.Where().
		WithPath([]string{"document_id"}).
		WithOperator(filters.Equal).
		WithValueText(documentID)

	_, err := s.client.Batch().ObjectsBatchDeleter().
		WithClassName(className).
		WithWhere(where).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("weaviate: failed to delete document %q: %w", documentID, err)
	}

	return nil
}

func (s *WeaviateStore) Store(ctx context.Context, collectionID string, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	if err := s.ensureClient(); err != nil {
		return err
	}

	batcher := s.client.Batch().ObjectsBatcher()

	for _, chunk := range chunks {
		metadataJSON, _ := json.Marshal(chunk.Metadata)
		batcher.WithObjects(&models.Object{
			Class: collectionID,
			Properties: map[string]interface{}{
				"obj_type":    "embedding",
				"document_id": chunk.DocumentID,
				"chunk_text":  chunk.Text,
				"chunk_index": chunk.ChunkIndex,
				"obj_metadata": string(metadataJSON),
			},
			Vector: chunk.Embedding,
		})
	}

	_, err := batcher.Do(ctx)
	if err != nil {
		return fmt.Errorf("weaviate: failed to batch insert %d chunks: %w", len(chunks), err)
	}

	logger.WithFields("class", collectionID, "chunks", len(chunks)).
		Debug("weaviate: stored embedding chunks")
	return nil
}

func (s *WeaviateStore) Query(ctx context.Context, collectionID string, embedding []float32, opts QueryOptions) ([]SearchResult, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	topK := opts.TopK
	if topK <= 0 {
		topK = 10
	}

	nearVector := s.client.GraphQL().NearVectorArgBuilder().
		WithVector(embedding)

	// Exclude sentinel objects from results.
	where := filters.Where().
		WithPath([]string{"obj_type"}).
		WithOperator(filters.Equal).
		WithValueText("embedding")

	result, err := s.client.GraphQL().Get().
		WithClassName(collectionID).
		WithFields(
			graphql.Field{Name: "document_id"},
			graphql.Field{Name: "chunk_text"},
			graphql.Field{Name: "chunk_index"},
			graphql.Field{Name: "obj_metadata"},
			graphql.Field{Name: "_additional", Fields: []graphql.Field{
				{Name: "distance"},
				{Name: "id"},
			}},
		).
		WithNearVector(nearVector).
		WithWhere(where).
		WithLimit(topK).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate: query failed: %w", err)
	}

	return parseWeaviateQueryResults(result, collectionID, opts.MinScore)
}

// --- Internal helpers ---

// readSentinel reads collection metadata from the sentinel object in a Weaviate class.
func (s *WeaviateStore) readSentinel(ctx context.Context, className string) (*Collection, error) {
	where := filters.Where().
		WithPath([]string{"obj_type"}).
		WithOperator(filters.Equal).
		WithValueText("collection_meta")

	result, err := s.client.GraphQL().Get().
		WithClassName(className).
		WithFields(
			graphql.Field{Name: "collection_id"},
			graphql.Field{Name: "collection_name"},
			graphql.Field{Name: "tenant_id"},
			graphql.Field{Name: "description"},
			graphql.Field{Name: "embedding_model"},
			graphql.Field{Name: "embedding_dimension"},
			graphql.Field{Name: "distance_metric"},
			graphql.Field{Name: "document_count"},
			graphql.Field{Name: "obj_metadata"},
			graphql.Field{Name: "created_at"},
			graphql.Field{Name: "updated_at"},
		).
		WithWhere(where).
		WithLimit(1).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate: failed to query sentinel: %w", err)
	}

	objects := extractGraphQLObjects(result, className)
	if len(objects) == 0 {
		return nil, fmt.Errorf("weaviate: no sentinel found in class %q", className)
	}

	obj := objects[0]
	coll := &Collection{
		ID:             getStringField(obj, "collection_id"),
		TenantID:       getStringField(obj, "tenant_id"),
		Name:           getStringField(obj, "collection_name"),
		Description:    getStringField(obj, "description"),
		EmbeddingModel: getStringField(obj, "embedding_model"),
		DistanceMetric: DistanceMetric(getStringField(obj, "distance_metric")),
	}

	if v := getFloatField(obj, "embedding_dimension"); v != 0 {
		coll.EmbeddingDimension = int(v)
	}
	if v := getFloatField(obj, "document_count"); v != 0 {
		coll.DocumentCount = int(v)
	}
	if v := getStringField(obj, "obj_metadata"); v != "" {
		var meta map[string]string
		if err := json.Unmarshal([]byte(v), &meta); err == nil {
			coll.Metadata = meta
		}
	}
	if v := getStringField(obj, "created_at"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			coll.CreatedAt = t
		}
	}
	if v := getStringField(obj, "updated_at"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			coll.UpdatedAt = t
		}
	}

	return coll, nil
}

// parseWeaviateQueryResults extracts SearchResults from a GraphQL response.
func parseWeaviateQueryResults(result *models.GraphQLResponse, className string, minScore float32) ([]SearchResult, error) {
	objects := extractGraphQLObjects(result, className)

	var results []SearchResult
	for _, obj := range objects {
		sr := SearchResult{
			DocumentID: getStringField(obj, "document_id"),
			ChunkText:  getStringField(obj, "chunk_text"),
			ChunkIndex: int(getFloatField(obj, "chunk_index")),
		}

		// Extract distance from _additional and convert to score.
		if additional, ok := obj["_additional"].(map[string]interface{}); ok {
			if dist, ok := additional["distance"].(float64); ok {
				// Weaviate returns distance; convert to similarity score.
				// For cosine: score = 1 - distance (distance is 0-2 for cosine).
				sr.Score = float32(1.0 - dist)
			}
		}

		if minScore > 0 && sr.Score < minScore {
			continue
		}

		if v := getStringField(obj, "obj_metadata"); v != "" {
			var meta map[string]string
			if err := json.Unmarshal([]byte(v), &meta); err == nil {
				sr.Metadata = meta
			}
		}

		results = append(results, sr)
	}

	return results, nil
}

// extractGraphQLObjects gets the object list from a Weaviate GraphQL response.
func extractGraphQLObjects(result *models.GraphQLResponse, className string) []map[string]interface{} {
	if result == nil || result.Data == nil {
		return nil
	}

	getData, ok := result.Data["Get"]
	if !ok {
		return nil
	}

	getMap, ok := getData.(map[string]interface{})
	if !ok {
		return nil
	}

	classData, ok := getMap[className]
	if !ok {
		return nil
	}

	items, ok := classData.([]interface{})
	if !ok {
		return nil
	}

	var objects []map[string]interface{}
	for _, item := range items {
		if obj, ok := item.(map[string]interface{}); ok {
			objects = append(objects, obj)
		}
	}
	return objects
}

// getStringField extracts a string from a GraphQL result object.
func getStringField(obj map[string]interface{}, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}

// getFloatField extracts a float64 from a GraphQL result object.
// Weaviate GraphQL returns numbers as float64.
func getFloatField(obj map[string]interface{}, key string) float64 {
	if v, ok := obj[key].(float64); ok {
		return v
	}
	return 0
}
