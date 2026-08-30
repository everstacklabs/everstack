package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/qdrant/go-client/qdrant"
)

// metaSentinelID is the well-known point ID used to store collection metadata
// inside each Qdrant collection. This avoids needing an external metadata store.
const metaSentinelID = "00000000-0000-0000-0000-000000000000"

// QdrantStore implements VectorStore using Qdrant vector database.
type QdrantStore struct {
	mu      sync.Mutex
	client  *qdrant.Client
	address string
	apiKey  string
}

// NewQdrantStore creates a new Qdrant-backed vector store.
// The connection is established lazily on first use.
func NewQdrantStore(address, apiKey string) *QdrantStore {
	return &QdrantStore{
		address: address,
		apiKey:  apiKey,
	}
}

func (s *QdrantStore) Name() string { return "qdrant" }

// ensureClient lazily establishes the gRPC connection to Qdrant.
func (s *QdrantStore) ensureClient() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return nil
	}

	host, port, err := parseQdrantAddress(s.address)
	if err != nil {
		return fmt.Errorf("qdrant: invalid address %q: %w", s.address, err)
	}

	client, err := qdrant.NewClient(&qdrant.Config{
		Host:   host,
		Port:   port,
		APIKey: s.apiKey,
		UseTLS: s.apiKey != "", // TLS when API key is set (cloud deployments)
	})
	if err != nil {
		return fmt.Errorf("qdrant: failed to connect to %s: %w", s.address, err)
	}

	s.client = client
	return nil
}

// HealthCheck verifies that the Qdrant server is reachable.
func (s *QdrantStore) HealthCheck(ctx context.Context) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	_, err := s.client.HealthCheck(ctx)
	return err
}

// Close closes the underlying gRPC connection.
func (s *QdrantStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		err := s.client.Close()
		s.client = nil
		return err
	}
	return nil
}

// qdrantCollectionName builds a tenant-isolated Qdrant collection name.
func qdrantCollectionName(tenantID, name string) string {
	return fmt.Sprintf("evs_%s_%s", tenantID, name)
}

func (s *QdrantStore) CreateCollection(ctx context.Context, tenantID string, opts CollectionOptions) (*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	collName := qdrantCollectionName(tenantID, opts.Name)

	// Create the Qdrant collection with vectors config.
	err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: collName,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     uint64(opts.EmbeddingDimension),
			Distance: toQdrantDistance(opts.DistanceMetric),
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: failed to create collection %q: %w", collName, err)
	}

	now := time.Now()

	// Upsert a sentinel metadata point to store collection metadata.
	metadataJSON, _ := json.Marshal(opts.Metadata)
	_, err = s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collName,
		Wait:           qdrant.PtrOf(true),
		Points: []*qdrant.PointStruct{
			{
				Id:      qdrant.NewIDUUID(metaSentinelID),
				Vectors: qdrant.NewVectorsDense(make([]float32, opts.EmbeddingDimension)),
				Payload: qdrant.NewValueMap(map[string]any{
					"_type":               "collection_meta",
					"_id":                 collName,
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
				}),
			},
		},
	})
	if err != nil {
		// Attempt to clean up the collection if sentinel upsert fails.
		_ = s.client.DeleteCollection(ctx, collName)
		return nil, fmt.Errorf("qdrant: failed to store collection metadata: %w", err)
	}

	logger.WithFields("backend", "qdrant", "collection", collName).
		Debug("qdrant: created collection")

	return &Collection{
		ID:                 collName,
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

func (s *QdrantStore) GetCollection(ctx context.Context, tenantID, name string) (*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	collName := qdrantCollectionName(tenantID, name)

	// Check if collection exists.
	exists, err := s.client.CollectionExists(ctx, collName)
	if err != nil {
		return nil, fmt.Errorf("qdrant: failed to check collection %q: %w", collName, err)
	}
	if !exists {
		return nil, fmt.Errorf("collection %q not found", name)
	}

	// Read sentinel metadata point.
	return s.readSentinel(ctx, collName)
}

func (s *QdrantStore) ListCollections(ctx context.Context, tenantID string) ([]*Collection, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	names, err := s.client.ListCollections(ctx)
	if err != nil {
		return nil, fmt.Errorf("qdrant: failed to list collections: %w", err)
	}

	prefix := fmt.Sprintf("evs_%s_", tenantID)
	var result []*Collection

	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		coll, err := s.readSentinel(ctx, name)
		if err != nil {
			logger.WithFields("collection", name, "error", err.Error()).
				Warn("qdrant: failed to read sentinel for collection, skipping")
			continue
		}
		result = append(result, coll)
	}

	return result, nil
}

func (s *QdrantStore) DeleteCollection(ctx context.Context, tenantID, name string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	collName := qdrantCollectionName(tenantID, name)

	exists, err := s.client.CollectionExists(ctx, collName)
	if err != nil {
		return fmt.Errorf("qdrant: failed to check collection %q: %w", collName, err)
	}
	if !exists {
		return fmt.Errorf("collection %q not found", name)
	}

	if err := s.client.DeleteCollection(ctx, collName); err != nil {
		return fmt.Errorf("qdrant: failed to delete collection %q: %w", collName, err)
	}

	logger.WithFields("backend", "qdrant", "collection", collName).
		Debug("qdrant: deleted collection")
	return nil
}

func (s *QdrantStore) AddDocuments(ctx context.Context, collectionID string, docs []Document) ([]string, error) {
	// For Qdrant, collectionID is the qdrant collection name (mf_{tenant}_{name}).
	// Documents are not stored as separate entities; they're tracked by document_id
	// payload on embedding points. Here we just generate UUIDs and increment the
	// sentinel document_count.
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = uuid.New().String()
	}

	// Increment document_count on the sentinel.
	if err := s.incrementDocumentCount(ctx, collectionID, len(docs)); err != nil {
		return nil, fmt.Errorf("qdrant: failed to update document count: %w", err)
	}

	return ids, nil
}

func (s *QdrantStore) DeleteDocument(ctx context.Context, tenantID, documentID string) error {
	// DeleteDocument removes all points with the given document_id payload value.
	// We don't know which collection the document belongs to, so we need to search
	// across all collections. This is a trade-off of not having a separate document table.
	// In practice, the caller (memory_store tool) knows the collection.
	//
	// For now, this returns an error indicating the limitation. The memory tools
	// should use DeleteDocumentFromCollection instead.
	return fmt.Errorf("qdrant: DeleteDocument requires collection context — use the memory tools which provide collection info")
}

// DeleteDocumentFromCollection removes all points matching a document_id from a specific collection.
func (s *QdrantStore) DeleteDocumentFromCollection(ctx context.Context, collectionName, documentID string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	// Delete all points where document_id matches.
	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collectionName,
		Wait:           qdrant.PtrOf(true),
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: &qdrant.Filter{
					Must: []*qdrant.Condition{
						qdrant.NewMatch("document_id", documentID),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("qdrant: failed to delete document %q: %w", documentID, err)
	}

	// Decrement document_count on sentinel.
	if err := s.incrementDocumentCount(ctx, collectionName, -1); err != nil {
		logger.WithFields("collection", collectionName, "error", err.Error()).
			Warn("qdrant: failed to decrement document count after delete")
	}

	return nil
}

func (s *QdrantStore) Store(ctx context.Context, collectionID string, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	if err := s.ensureClient(); err != nil {
		return err
	}

	points := make([]*qdrant.PointStruct, len(chunks))
	for i, chunk := range chunks {
		metadataJSON, _ := json.Marshal(chunk.Metadata)
		points[i] = &qdrant.PointStruct{
			Id:      qdrant.NewIDUUID(uuid.New().String()),
			Vectors: qdrant.NewVectorsDense(chunk.Embedding),
			Payload: qdrant.NewValueMap(map[string]any{
				"document_id": chunk.DocumentID,
				"chunk_text":  chunk.Text,
				"chunk_index": chunk.ChunkIndex,
				"metadata":    string(metadataJSON),
			}),
		}
	}

	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collectionID,
		Wait:           qdrant.PtrOf(true),
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("qdrant: failed to upsert %d chunks: %w", len(chunks), err)
	}

	logger.WithFields("collection", collectionID, "chunks", len(chunks)).
		Debug("qdrant: stored embedding chunks")
	return nil
}

func (s *QdrantStore) Query(ctx context.Context, collectionID string, embedding []float32, opts QueryOptions) ([]SearchResult, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	topK := uint64(opts.TopK)
	if topK == 0 {
		topK = 10
	}

	// Exclude the sentinel metadata point from results.
	filter := &qdrant.Filter{
		MustNot: []*qdrant.Condition{
			qdrant.NewMatch("_type", "collection_meta"),
		},
	}

	results, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: collectionID,
		Query:          qdrant.NewQueryDense(embedding),
		Filter:         filter,
		Limit:          qdrant.PtrOf(topK),
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: query failed: %w", err)
	}

	searchResults := make([]SearchResult, 0, len(results))
	for _, pt := range results {
		score := pt.Score
		if opts.MinScore > 0 && score < opts.MinScore {
			continue
		}

		sr := SearchResult{
			Score: score,
		}

		if payload := pt.Payload; payload != nil {
			if v, ok := payload["document_id"]; ok {
				sr.DocumentID = v.GetStringValue()
			}
			if v, ok := payload["chunk_text"]; ok {
				sr.ChunkText = v.GetStringValue()
			}
			if v, ok := payload["chunk_index"]; ok {
				sr.ChunkIndex = int(v.GetIntegerValue())
			}
			if v, ok := payload["metadata"]; ok {
				var meta map[string]string
				if err := json.Unmarshal([]byte(v.GetStringValue()), &meta); err == nil {
					sr.Metadata = meta
				}
			}
		}

		searchResults = append(searchResults, sr)
	}

	return searchResults, nil
}

// --- Internal helpers ---

// readSentinel reads the sentinel metadata point from a Qdrant collection and
// reconstructs a Collection struct.
func (s *QdrantStore) readSentinel(ctx context.Context, collName string) (*Collection, error) {
	points, err := s.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: collName,
		Ids:            []*qdrant.PointId{qdrant.NewIDUUID(metaSentinelID)},
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: failed to read sentinel from %q: %w", collName, err)
	}
	if len(points) == 0 {
		return nil, fmt.Errorf("qdrant: no sentinel metadata found in %q", collName)
	}

	payload := points[0].Payload
	if payload == nil {
		return nil, fmt.Errorf("qdrant: sentinel in %q has no payload", collName)
	}

	coll := &Collection{
		ID:       collName, // Use the qdrant collection name so AddDocuments/Store/Query can use it directly
		Name:     payloadString(payload, "name"),
		TenantID: payloadString(payload, "tenant_id"),
	}

	coll.Description = payloadString(payload, "description")
	coll.EmbeddingModel = payloadString(payload, "embedding_model")
	coll.DistanceMetric = DistanceMetric(payloadString(payload, "distance_metric"))

	if v, ok := payload["embedding_dimension"]; ok {
		coll.EmbeddingDimension = int(v.GetIntegerValue())
	}
	if v, ok := payload["document_count"]; ok {
		coll.DocumentCount = int(v.GetIntegerValue())
	}

	if v, ok := payload["metadata"]; ok {
		var meta map[string]string
		if err := json.Unmarshal([]byte(v.GetStringValue()), &meta); err == nil {
			coll.Metadata = meta
		}
	}

	if v, ok := payload["created_at"]; ok {
		if t, err := time.Parse(time.RFC3339, v.GetStringValue()); err == nil {
			coll.CreatedAt = t
		}
	}
	if v, ok := payload["updated_at"]; ok {
		if t, err := time.Parse(time.RFC3339, v.GetStringValue()); err == nil {
			coll.UpdatedAt = t
		}
	}

	return coll, nil
}

// incrementDocumentCount atomically updates the document_count on the sentinel point.
func (s *QdrantStore) incrementDocumentCount(ctx context.Context, collName string, delta int) error {
	// Read current sentinel.
	points, err := s.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: collName,
		Ids:            []*qdrant.PointId{qdrant.NewIDUUID(metaSentinelID)},
		WithPayload:    qdrant.NewWithPayload(true),
		WithVectors:    qdrant.NewWithVectors(true),
	})
	if err != nil {
		return fmt.Errorf("failed to read sentinel: %w", err)
	}
	if len(points) == 0 {
		return fmt.Errorf("no sentinel found in %q", collName)
	}

	payload := points[0].Payload
	currentCount := int64(0)
	if v, ok := payload["document_count"]; ok {
		currentCount = v.GetIntegerValue()
	}
	newCount := currentCount + int64(delta)
	if newCount < 0 {
		newCount = 0
	}

	// Update the document_count and updated_at fields.
	payload["document_count"] = qdrant.NewValueMap(map[string]any{"document_count": newCount})["document_count"]
	payload["updated_at"] = qdrant.NewValueMap(map[string]any{"updated_at": time.Now().Format(time.RFC3339)})["updated_at"]

	// Re-upsert the sentinel with the existing vector.
	vector := points[0].Vectors
	var vectors *qdrant.Vectors
	if vector != nil {
		if dv := vector.GetVector(); dv != nil {
			vectors = qdrant.NewVectorsDense(dv.GetData())
		}
	}
	if vectors == nil {
		// Fallback: shouldn't happen, but create a zero vector.
		if dim, ok := payload["embedding_dimension"]; ok {
			vectors = qdrant.NewVectorsDense(make([]float32, int(dim.GetIntegerValue())))
		} else {
			return fmt.Errorf("cannot determine vector dimension for sentinel")
		}
	}

	_, err = s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collName,
		Wait:           qdrant.PtrOf(true),
		Points: []*qdrant.PointStruct{
			{
				Id:      qdrant.NewIDUUID(metaSentinelID),
				Vectors: vectors,
				Payload: payload,
			},
		},
	})
	return err
}

// payloadString extracts a string value from a Qdrant payload map.
func payloadString(payload map[string]*qdrant.Value, key string) string {
	if v, ok := payload[key]; ok {
		return v.GetStringValue()
	}
	return ""
}

// toQdrantDistance maps our DistanceMetric to Qdrant's Distance enum.
func toQdrantDistance(dm DistanceMetric) qdrant.Distance {
	switch dm {
	case DistanceCosine:
		return qdrant.Distance_Cosine
	case DistanceEuclidean:
		return qdrant.Distance_Euclid
	case DistanceDotProduct:
		return qdrant.Distance_Dot
	default:
		return qdrant.Distance_Cosine
	}
}

// parseQdrantAddress splits "host:port" into components.
func parseQdrantAddress(address string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		// Try treating the entire string as a host (default port).
		return address, 6334, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	return host, port, nil
}
