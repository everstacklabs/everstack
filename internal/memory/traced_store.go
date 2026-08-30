package memory

import (
	"context"

	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
)

// tracedVectorStore wraps any VectorStore backend and emits telemetry spans for
// the read/write hot paths (query/store/add_documents). Collection-management
// calls pass straight through: those are operational, not telemetry, per the
// emitter allowlist (traces-module-replan section 6). Because it wraps the
// interface, every backend (pgvector/qdrant/pinecone/weaviate) is instrumented
// once.
type tracedVectorStore struct{ inner VectorStore }

// NewTracedVectorStore wraps a VectorStore with span instrumentation. Returns nil
// if inner is nil so callers can wrap unconditionally.
func NewTracedVectorStore(inner VectorStore) VectorStore {
	if inner == nil {
		return nil
	}
	return &tracedVectorStore{inner: inner}
}

func (t *tracedVectorStore) Query(ctx context.Context, collectionID string, embedding []float32, opts QueryOptions) ([]SearchResult, error) {
	ctx, span := telemetry.StartVectorStoreSpan(ctx, "query", t.inner.Name())
	defer span.End()
	span.SetAttributes(
		attribute.String(attrs.VectorCollection, collectionID),
		attribute.Int(attrs.VectorTopK, opts.TopK),
		attribute.Int(attrs.EmbeddingDimension, len(embedding)),
	)
	res, err := t.inner.Query(ctx, collectionID, embedding, opts)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int(attrs.VectorResultCount, len(res)))
	return res, nil
}

func (t *tracedVectorStore) Store(ctx context.Context, collectionID string, chunks []Chunk) error {
	ctx, span := telemetry.StartVectorStoreSpan(ctx, "store", t.inner.Name())
	defer span.End()
	span.SetAttributes(
		attribute.String(attrs.VectorCollection, collectionID),
		attribute.Int(attrs.VectorResultCount, len(chunks)),
	)
	if err := t.inner.Store(ctx, collectionID, chunks); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	return nil
}

func (t *tracedVectorStore) AddDocuments(ctx context.Context, collectionID string, docs []Document) ([]string, error) {
	ctx, span := telemetry.StartVectorStoreSpan(ctx, "add_documents", t.inner.Name())
	defer span.End()
	span.SetAttributes(
		attribute.String(attrs.VectorCollection, collectionID),
		attribute.Int(attrs.VectorResultCount, len(docs)),
	)
	ids, err := t.inner.AddDocuments(ctx, collectionID, docs)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	return ids, nil
}

// Collection management pass-throughs (operational, not traced).
func (t *tracedVectorStore) CreateCollection(ctx context.Context, tenantID string, opts CollectionOptions) (*Collection, error) {
	return t.inner.CreateCollection(ctx, tenantID, opts)
}

func (t *tracedVectorStore) GetCollection(ctx context.Context, tenantID, name string) (*Collection, error) {
	return t.inner.GetCollection(ctx, tenantID, name)
}

func (t *tracedVectorStore) ListCollections(ctx context.Context, tenantID string) ([]*Collection, error) {
	return t.inner.ListCollections(ctx, tenantID)
}

func (t *tracedVectorStore) DeleteCollection(ctx context.Context, tenantID, name string) error {
	return t.inner.DeleteCollection(ctx, tenantID, name)
}

func (t *tracedVectorStore) DeleteDocument(ctx context.Context, tenantID, documentID string) error {
	return t.inner.DeleteDocument(ctx, tenantID, documentID)
}

func (t *tracedVectorStore) Name() string { return t.inner.Name() }

// tracedEmbedder wraps an EmbedderInterface and emits embedding spans (M1-T7).
type tracedEmbedder struct{ inner EmbedderInterface }

// NewTracedEmbedder wraps an EmbedderInterface with span instrumentation.
func NewTracedEmbedder(inner EmbedderInterface) EmbedderInterface {
	if inner == nil {
		return nil
	}
	return &tracedEmbedder{inner: inner}
}

func (t *tracedEmbedder) Embed(ctx context.Context, model, text string) ([]float32, error) {
	ctx, span := telemetry.StartMemoryEmbeddingSpan(ctx, model)
	defer span.End()
	span.SetAttributes(attribute.Int(attrs.EmbeddingInputCount, 1))
	vec, err := t.inner.Embed(ctx, model, text)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int(attrs.EmbeddingDimension, len(vec)))
	return vec, nil
}

func (t *tracedEmbedder) EmbedBatch(ctx context.Context, model string, texts []string) ([][]float32, error) {
	ctx, span := telemetry.StartMemoryEmbeddingSpan(ctx, model)
	defer span.End()
	span.SetAttributes(attribute.Int(attrs.EmbeddingInputCount, len(texts)))
	vecs, err := t.inner.EmbedBatch(ctx, model, texts)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	if len(vecs) > 0 {
		span.SetAttributes(attribute.Int(attrs.EmbeddingDimension, len(vecs[0])))
	}
	return vecs, nil
}
