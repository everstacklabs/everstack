package memory

import (
	"context"
	"time"
)

// VectorStore is the pluggable abstraction for vector-backed memory stores.
type VectorStore interface {
	// CreateCollection creates a new vector collection for a tenant.
	CreateCollection(ctx context.Context, tenantID string, opts CollectionOptions) (*Collection, error)
	// GetCollection retrieves a collection by name.
	GetCollection(ctx context.Context, tenantID, name string) (*Collection, error)
	// ListCollections lists all collections for a tenant.
	ListCollections(ctx context.Context, tenantID string) ([]*Collection, error)
	// DeleteCollection deletes a collection and all its data.
	DeleteCollection(ctx context.Context, tenantID, name string) error
	// AddDocuments adds documents to a collection, returning generated IDs.
	AddDocuments(ctx context.Context, collectionID string, docs []Document) ([]string, error)
	// DeleteDocument deletes a single document and its embeddings, scoped by tenant.
	DeleteDocument(ctx context.Context, tenantID, documentID string) error
	// Store stores pre-computed embedding chunks into a collection.
	Store(ctx context.Context, collectionID string, chunks []Chunk) error
	// Query searches for similar vectors in a collection.
	Query(ctx context.Context, collectionID string, embedding []float32, opts QueryOptions) ([]SearchResult, error)
	// Name returns the backend name (e.g., "pgvector", "qdrant").
	Name() string
}

// CollectionOptions configures a new collection.
type CollectionOptions struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	EmbeddingModel     string            `json:"embedding_model"`
	EmbeddingDimension int               `json:"embedding_dimension"`
	DistanceMetric     DistanceMetric    `json:"distance_metric"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// Collection represents a vector collection.
type Collection struct {
	ID                 string            `json:"id"`
	TenantID           string            `json:"tenant_id"`
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	EmbeddingModel     string            `json:"embedding_model"`
	EmbeddingDimension int               `json:"embedding_dimension"`
	DistanceMetric     DistanceMetric    `json:"distance_metric"`
	DocumentCount      int               `json:"document_count"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// Document represents a source document to be embedded and stored.
type Document struct {
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Source   string            `json:"source,omitempty"`
}

// Chunk represents a piece of text with its embedding vector.
type Chunk struct {
	DocumentID string            `json:"document_id"`
	Text       string            `json:"text"`
	ChunkIndex int               `json:"chunk_index"`
	Embedding  []float32         `json:"embedding"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// QueryOptions configures a vector similarity search.
type QueryOptions struct {
	TopK           int               `json:"top_k"`
	MinScore       float32           `json:"min_score,omitempty"`
	MetadataFilter map[string]string `json:"metadata_filter,omitempty"`
}

// SearchResult represents a single search result with score.
type SearchResult struct {
	DocumentID string            `json:"document_id"`
	ChunkText  string            `json:"chunk_text"`
	ChunkIndex int               `json:"chunk_index"`
	Score      float32           `json:"score"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// DistanceMetric defines how vector similarity is measured.
type DistanceMetric string

const (
	DistanceCosine     DistanceMetric = "cosine"
	DistanceEuclidean  DistanceMetric = "euclidean"
	DistanceDotProduct DistanceMetric = "dot_product"
)

// EmbedderInterface abstracts embedding generation for testability.
type EmbedderInterface interface {
	Embed(ctx context.Context, model, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// MemoryConfig holds configuration for the memory subsystem.
type MemoryConfig struct {
	Backend               string         `json:"backend"` // "pgvector" or "qdrant"
	DefaultEmbeddingModel string         `json:"default_embedding_model"`
	PgVector              PgVectorConfig `json:"pgvector,omitempty"`
	Qdrant                QdrantConfig   `json:"qdrant,omitempty"`
}

// PgVectorConfig holds pgvector-specific configuration.
type PgVectorConfig struct {
	// Uses the existing database.postgres.dsn
}

// QdrantConfig holds Qdrant-specific configuration.
type QdrantConfig struct {
	Address string `json:"address"`
	APIKey  string `json:"api_key,omitempty"`
}
