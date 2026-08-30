package memory

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// fakeStore is a minimal VectorStore for instrumentation tests.
type fakeStore struct{ name string }

func (f *fakeStore) CreateCollection(context.Context, string, CollectionOptions) (*Collection, error) {
	return &Collection{}, nil
}
func (f *fakeStore) GetCollection(context.Context, string, string) (*Collection, error) {
	return &Collection{}, nil
}
func (f *fakeStore) ListCollections(context.Context, string) ([]*Collection, error) { return nil, nil }
func (f *fakeStore) DeleteCollection(context.Context, string, string) error         { return nil }
func (f *fakeStore) AddDocuments(context.Context, string, []Document) ([]string, error) {
	return []string{"a"}, nil
}
func (f *fakeStore) DeleteDocument(context.Context, string, string) error { return nil }
func (f *fakeStore) Store(context.Context, string, []Chunk) error         { return nil }
func (f *fakeStore) Query(context.Context, string, []float32, QueryOptions) ([]SearchResult, error) {
	return []SearchResult{{DocumentID: "d1"}, {DocumentID: "d2"}}, nil
}
func (f *fakeStore) Name() string { return f.name }

type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(context.Context, string, string) ([]float32, error) {
	return []float32{0, 1, 2}, nil
}
func (f *fakeEmbedder) EmbedBatch(context.Context, string, []string) ([][]float32, error) {
	return [][]float32{{0, 1, 2}}, nil
}

func withTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := telemetry.GetGlobalTracerProvider()
	telemetry.SetGlobalTracerProvider(tp)
	t.Cleanup(func() { telemetry.SetGlobalTracerProvider(prev) })
	return exporter
}

func TestTracedVectorStoreQuery(t *testing.T) {
	exporter := withTestTracer(t)
	store := NewTracedVectorStore(&fakeStore{name: "pgvector"})

	res, err := store.Query(context.Background(), "col1", []float32{1, 2, 3, 4}, QueryOptions{TopK: 5})
	if err != nil || len(res) != 2 {
		t.Fatalf("query: res=%d err=%v", len(res), err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "vector.query" {
		t.Fatalf("expected vector.query span, got %+v", spans)
	}
	got := map[string]string{}
	geti := map[string]int64{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
		geti[string(a.Key)] = a.Value.AsInt64()
	}
	if got[attrs.ObservationType] != string(telemetry.ObservationTypeRetriever) {
		t.Errorf("observation.type = %q, want RETRIEVER", got[attrs.ObservationType])
	}
	if got[attrs.VectorBackend] != "pgvector" || got[attrs.VectorCollection] != "col1" {
		t.Errorf("backend/collection wrong: %+v", got)
	}
	if geti[attrs.VectorTopK] != 5 || geti[attrs.VectorResultCount] != 2 || geti[attrs.EmbeddingDimension] != 4 {
		t.Errorf("numeric attrs wrong: %+v", geti)
	}
}

func TestTracedEmbedder(t *testing.T) {
	exporter := withTestTracer(t)
	emb := NewTracedEmbedder(&fakeEmbedder{})

	if _, err := emb.Embed(context.Background(), "brain-embedding-1", "hello"); err != nil {
		t.Fatalf("embed: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "embedding.embed" {
		t.Fatalf("expected embedding.embed span, got %+v", spans)
	}
	got := map[string]string{}
	geti := map[string]int64{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
		geti[string(a.Key)] = a.Value.AsInt64()
	}
	if got[attrs.ObservationType] != string(telemetry.ObservationTypeEmbedding) {
		t.Errorf("observation.type = %q, want EMBEDDING", got[attrs.ObservationType])
	}
	if got[attrs.EmbeddingModel] != "brain-embedding-1" {
		t.Errorf("model wrong: %+v", got)
	}
	if geti[attrs.EmbeddingDimension] != 3 || geti[attrs.EmbeddingInputCount] != 1 {
		t.Errorf("numeric attrs wrong: %+v", geti)
	}
}
