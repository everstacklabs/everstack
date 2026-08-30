package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// PgVectorStore implements VectorStore using PostgreSQL with pgvector extension.
type PgVectorStore struct {
	db *sqlx.DB
}

// EnsurePgVector idempotently sets up the pgvector extension and the embedding
// column on the memory_embeddings table. Call this at startup when the memory
// feature is enabled.
//
// The column is created as `vector` (no fixed dimension) so that collections
// using different embedding models with different dimensions can coexist in the
// same table. Each collection enforces its own dimension via the application layer.
func EnsurePgVector(ctx context.Context, db *sqlx.DB) error {
	// 1. Check whether the pgvector extension is available on this Postgres installation.
	var available bool
	if err := db.GetContext(ctx, &available,
		`SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`); err != nil {
		return fmt.Errorf("pgvector: failed to check pg_available_extensions: %w", err)
	}
	if !available {
		return fmt.Errorf("pgvector: the 'vector' extension is not available on this PostgreSQL installation — install pgvector first")
	}

	// 2. Create extension.
	if _, err := db.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("pgvector: failed to create extension: %w", err)
	}

	// 3. Add embedding column (dimensionless) if it doesn't exist.
	// A dimensionless vector column accepts embeddings of any size, which is
	// required when multiple embedding models with different dimensions are
	// configured. Similarity indexes (ivfflat/hnsw) require a fixed dimension
	// and are created per-collection-dimension as data is inserted.
	if _, err := db.ExecContext(ctx,
		`ALTER TABLE memory_embeddings ADD COLUMN IF NOT EXISTS embedding vector`); err != nil {
		return fmt.Errorf("pgvector: failed to add embedding column: %w", err)
	}

	logger.Info("pgvector: extension and embedding column ensured")
	return nil
}

// NewPgVectorStore creates a new pgvector-backed vector store.
// Returns an error if the pgvector extension is not installed (the embedding
// column won't exist, so Store/Query would fail at runtime).
func NewPgVectorStore(db *sqlx.DB) (*PgVectorStore, error) {
	var hasVector bool
	err := db.Get(&hasVector,
		`SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'everstack'
			  AND table_name = 'memory_embeddings'
			  AND column_name = 'embedding'
		)`)
	if err != nil {
		return nil, fmt.Errorf("pgvector: failed to check embedding column: %w", err)
	}
	if !hasVector {
		return nil, fmt.Errorf("pgvector: embedding column not found — install the pgvector extension and re-run migrations")
	}
	return &PgVectorStore{db: db}, nil
}

func (s *PgVectorStore) Name() string { return "pgvector" }

func (s *PgVectorStore) CreateCollection(ctx context.Context, tenantID string, opts CollectionOptions) (*Collection, error) {
	id := uuid.New().String()
	now := time.Now()

	metadataJSON, _ := json.Marshal(opts.Metadata)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_collections
		 (id, tenant_id, name, description, embedding_model, embedding_dimension, distance_metric, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, tenantID, opts.Name, opts.Description, opts.EmbeddingModel,
		opts.EmbeddingDimension, string(opts.DistanceMetric), metadataJSON, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create collection: %w", err)
	}

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

func (s *PgVectorStore) GetCollection(ctx context.Context, tenantID, name string) (*Collection, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	var c collectionRow
	err := s.db.GetContext(ctx, &c,
		`SELECT id, tenant_id, name, description, embedding_model, embedding_dimension,
		        distance_metric, metadata, document_count, created_at, updated_at
		 FROM memory_collections
		 WHERE tenant_id = $1 AND name = $2`, tenantID, name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("collection %q not found", name)
		}
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}
	return c.toCollection(), nil
}

func (s *PgVectorStore) ListCollections(ctx context.Context, tenantID string) ([]*Collection, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	var rows []collectionRow
	err := s.db.SelectContext(ctx, &rows,
		`SELECT id, tenant_id, name, description, embedding_model, embedding_dimension,
		        distance_metric, metadata, document_count, created_at, updated_at
		 FROM memory_collections
		 WHERE tenant_id = $1
		 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to list collections: %w", err)
	}

	result := make([]*Collection, len(rows))
	for i := range rows {
		result[i] = rows[i].toCollection()
	}
	return result, nil
}

func (s *PgVectorStore) DeleteCollection(ctx context.Context, tenantID, name string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant id is required")
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_collections WHERE tenant_id = $1 AND name = $2`,
		tenantID, name)
	if err != nil {
		return fmt.Errorf("failed to delete collection: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	return nil
}

func (s *PgVectorStore) AddDocuments(ctx context.Context, collectionID string, docs []Document) ([]string, error) {
	ids := make([]string, len(docs))
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for i, doc := range docs {
		id := uuid.New().String()
		ids[i] = id
		metadataJSON, _ := json.Marshal(doc.Metadata)

		// Get tenant_id from collection
		var tenantID string
		err := tx.GetContext(ctx, &tenantID,
			`SELECT tenant_id FROM memory_collections WHERE id = $1`, collectionID)
		if err != nil {
			return nil, fmt.Errorf("collection not found: %w", err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO memory_documents (id, collection_id, tenant_id, content, metadata, source, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
			id, collectionID, tenantID, doc.Content, metadataJSON, doc.Source)
		if err != nil {
			return nil, fmt.Errorf("failed to insert document: %w", err)
		}
	}

	// Update document count
	_, err = tx.ExecContext(ctx,
		`UPDATE memory_collections SET document_count = document_count + $1, updated_at = NOW() WHERE id = $2`,
		len(docs), collectionID)
	if err != nil {
		return nil, fmt.Errorf("failed to update document count: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}

	return ids, nil
}

func (s *PgVectorStore) DeleteDocument(ctx context.Context, tenantID, documentID string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant id is required")
	}
	// Cascading delete handles embeddings via FK
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_documents WHERE tenant_id = $1 AND id = $2`, tenantID, documentID)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("document not found")
	}
	return nil
}

func (s *PgVectorStore) Store(ctx context.Context, collectionID string, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, chunk := range chunks {
		id := uuid.New().String()
		metadataJSON, _ := json.Marshal(chunk.Metadata)

		// Format embedding as pgvector string
		embeddingStr := formatPgVector(chunk.Embedding)

		_, err := tx.ExecContext(ctx,
			`INSERT INTO memory_embeddings
			 (id, document_id, collection_id, chunk_text, chunk_index, embedding, metadata, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6::vector, $7, NOW())`,
			id, chunk.DocumentID, collectionID, chunk.Text, chunk.ChunkIndex,
			embeddingStr, metadataJSON)
		if err != nil {
			return fmt.Errorf("failed to insert embedding: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	logger.WithFields("collection_id", collectionID, "chunks", len(chunks)).
		Debug("pgvector: stored embedding chunks")

	return nil
}

func (s *PgVectorStore) Query(ctx context.Context, collectionID string, embedding []float32, opts QueryOptions) ([]SearchResult, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 10
	}

	embeddingStr := formatPgVector(embedding)

	query := `SELECT e.document_id, e.chunk_text, e.chunk_index, e.metadata,
	                  1 - (e.embedding <=> $1::vector) AS score
	          FROM memory_embeddings e
	          WHERE e.collection_id = $2`

	args := []interface{}{embeddingStr, collectionID}
	argIdx := 3

	if opts.MinScore > 0 {
		query += fmt.Sprintf(" AND 1 - (e.embedding <=> $1::vector) >= $%d", argIdx)
		args = append(args, opts.MinScore)
		argIdx++
	}

	query += " ORDER BY e.embedding <=> $1::vector LIMIT $" + fmt.Sprintf("%d", argIdx)
	args = append(args, topK)

	var rows []embeddingRow
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query embeddings: %w", err)
	}

	results := make([]SearchResult, len(rows))
	for i, row := range rows {
		var metadata map[string]string
		if len(row.Metadata) > 0 {
			json.Unmarshal(row.Metadata, &metadata)
		}
		results[i] = SearchResult{
			DocumentID: row.DocumentID,
			ChunkText:  row.ChunkText,
			ChunkIndex: row.ChunkIndex,
			Score:      row.Score,
			Metadata:   metadata,
		}
	}

	return results, nil
}

// Internal row types for database scanning
type collectionRow struct {
	ID                 string         `db:"id"`
	TenantID           string         `db:"tenant_id"`
	Name               string         `db:"name"`
	Description        sql.NullString `db:"description"`
	EmbeddingModel     string         `db:"embedding_model"`
	EmbeddingDimension int            `db:"embedding_dimension"`
	DistanceMetric     string         `db:"distance_metric"`
	Metadata           []byte         `db:"metadata"`
	DocumentCount      int            `db:"document_count"`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

func (r *collectionRow) toCollection() *Collection {
	var metadata map[string]string
	if len(r.Metadata) > 0 {
		json.Unmarshal(r.Metadata, &metadata)
	}
	return &Collection{
		ID:                 r.ID,
		TenantID:           r.TenantID,
		Name:               r.Name,
		Description:        r.Description.String,
		EmbeddingModel:     r.EmbeddingModel,
		EmbeddingDimension: r.EmbeddingDimension,
		DistanceMetric:     DistanceMetric(r.DistanceMetric),
		DocumentCount:      r.DocumentCount,
		Metadata:           metadata,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

type embeddingRow struct {
	DocumentID string  `db:"document_id"`
	ChunkText  string  `db:"chunk_text"`
	ChunkIndex int     `db:"chunk_index"`
	Metadata   []byte  `db:"metadata"`
	Score      float32 `db:"score"`
}

// formatPgVector formats a float32 slice as a pgvector literal string.
func formatPgVector(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = fmt.Sprintf("%f", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
