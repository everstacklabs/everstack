package v1

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/memory"
	memorypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/memory/v1"
)

// tenantCtx returns a background context with the given tenant id installed
// the way the auth middleware would. Tests use this everywhere they exercise
// a handler — passing tenant id only in the proto body is no longer accepted
// and would (correctly) fail with PermissionDenied.
func tenantCtx(tid string) context.Context {
	return contextkeys.WithTenantID(context.Background(), tid)
}

// ─── Mock VectorStore ──────────────────────────────────────────────

type mockVectorStore struct {
	collections  map[string]*memory.Collection
	storedChunks []memory.Chunk
	queryResults []memory.SearchResult
}

func newMockVectorStore() *mockVectorStore {
	return &mockVectorStore{
		collections: make(map[string]*memory.Collection),
	}
}

func (m *mockVectorStore) Name() string { return "mock" }

func (m *mockVectorStore) CreateCollection(_ context.Context, tenantID string, opts memory.CollectionOptions) (*memory.Collection, error) {
	c := &memory.Collection{
		ID:                 "coll-" + opts.Name,
		TenantID:           tenantID,
		Name:               opts.Name,
		Description:        opts.Description,
		EmbeddingModel:     opts.EmbeddingModel,
		EmbeddingDimension: opts.EmbeddingDimension,
		DistanceMetric:     opts.DistanceMetric,
	}
	m.collections[opts.Name] = c
	return c, nil
}

func (m *mockVectorStore) GetCollection(_ context.Context, _ string, name string) (*memory.Collection, error) {
	if c, ok := m.collections[name]; ok {
		return c, nil
	}
	return nil, context.DeadlineExceeded
}

func (m *mockVectorStore) ListCollections(_ context.Context, _ string) ([]*memory.Collection, error) {
	var result []*memory.Collection
	for _, c := range m.collections {
		result = append(result, c)
	}
	return result, nil
}

func (m *mockVectorStore) DeleteCollection(_ context.Context, _, name string) error {
	if _, ok := m.collections[name]; !ok {
		return context.DeadlineExceeded
	}
	delete(m.collections, name)
	return nil
}

func (m *mockVectorStore) AddDocuments(_ context.Context, _ string, docs []memory.Document) ([]string, error) {
	ids := make([]string, len(docs))
	for i := range docs {
		ids[i] = "doc-" + string(rune('a'+i))
	}
	return ids, nil
}

func (m *mockVectorStore) DeleteDocument(_ context.Context, _, _ string) error { return nil }

func (m *mockVectorStore) Store(_ context.Context, _ string, chunks []memory.Chunk) error {
	m.storedChunks = append(m.storedChunks, chunks...)
	return nil
}

func (m *mockVectorStore) Query(_ context.Context, _ string, _ []float32, _ memory.QueryOptions) ([]memory.SearchResult, error) {
	return m.queryResults, nil
}

// ─── Mock Embedder ─────────────────────────────────────────────────

type mockEmbedder struct {
	dimension int
}

func (m *mockEmbedder) Embed(_ context.Context, _, _ string) ([]float32, error) {
	return make([]float32, m.dimension), nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, _ string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, m.dimension)
	}
	return result, nil
}

// ─── Helper ────────────────────────────────────────────────────────

func newTestServer() (*Server, *mockVectorStore) {
	store := newMockVectorStore()
	srv := CreateServerWithDeps(context.Background(), store, &mockEmbedder{dimension: 128}, "test-model", 128)
	return srv, store
}

// ─── Tests ─────────────────────────────────────────────────────────

func TestCreateCollection_Success(t *testing.T) {
	srv, store := newTestServer()

	resp, err := srv.CreateCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.CreateCollectionRequest{
		TenantId: "tenant-1",
		Name:     "my-collection",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Msg.Collection.Name != "my-collection" {
		t.Fatalf("expected name 'my-collection', got %q", resp.Msg.Collection.Name)
	}

	if _, ok := store.collections["my-collection"]; !ok {
		t.Fatal("collection not found in store")
	}
}

func TestCreateCollection_MissingName(t *testing.T) {
	srv, _ := newTestServer()

	_, err := srv.CreateCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.CreateCollectionRequest{
		TenantId: "tenant-1",
	}))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestGetCollection_NotFound(t *testing.T) {
	srv, _ := newTestServer()

	_, err := srv.GetCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.GetCollectionRequest{
		TenantId: "tenant-1",
		Name:     "nonexistent",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent collection")
	}
}

func TestListCollections_Empty(t *testing.T) {
	srv, _ := newTestServer()

	resp, err := srv.ListCollections(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.ListCollectionsRequest{
		TenantId: "tenant-1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.Collections) != 0 {
		t.Fatalf("expected 0 collections, got %d", len(resp.Msg.Collections))
	}
}

func TestListCollections_WithData(t *testing.T) {
	srv, store := newTestServer()

	store.collections["coll-a"] = &memory.Collection{ID: "1", Name: "coll-a", TenantID: "tenant-1"}
	store.collections["coll-b"] = &memory.Collection{ID: "2", Name: "coll-b", TenantID: "tenant-1"}

	resp, err := srv.ListCollections(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.ListCollectionsRequest{
		TenantId: "tenant-1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Msg.Collections) != 2 {
		t.Fatalf("expected 2 collections, got %d", len(resp.Msg.Collections))
	}
}

func TestDeleteCollection_Success(t *testing.T) {
	srv, store := newTestServer()

	store.collections["to-delete"] = &memory.Collection{ID: "1", Name: "to-delete"}

	resp, err := srv.DeleteCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.DeleteCollectionRequest{
		TenantId: "tenant-1",
		Name:     "to-delete",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Msg.Success {
		t.Fatal("expected success=true")
	}
	if _, ok := store.collections["to-delete"]; ok {
		t.Fatal("collection should have been deleted")
	}
}

func TestAddDocuments_Success(t *testing.T) {
	srv, store := newTestServer()

	store.collections["my-coll"] = &memory.Collection{
		ID:             "coll-1",
		Name:           "my-coll",
		EmbeddingModel: "test-model",
	}

	resp, err := srv.AddDocuments(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.AddDocumentsRequest{
		TenantId:       "tenant-1",
		CollectionName: "my-coll",
		Documents: []*memorypb.Document{
			{Content: "Hello world. This is a test document.", Source: "test"},
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Msg.DocumentIds) == 0 {
		t.Fatal("expected document IDs")
	}
	if resp.Msg.ChunksCreated == 0 {
		t.Fatal("expected chunks to be created")
	}
	if len(store.storedChunks) == 0 {
		t.Fatal("expected chunks to be stored")
	}
}

func TestQueryCollection_Success(t *testing.T) {
	srv, store := newTestServer()

	store.collections["my-coll"] = &memory.Collection{
		ID:             "coll-1",
		Name:           "my-coll",
		EmbeddingModel: "test-model",
	}
	store.queryResults = []memory.SearchResult{
		{DocumentID: "doc-1", ChunkText: "relevant content", Score: 0.9},
	}

	resp, err := srv.QueryCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.QueryCollectionRequest{
		TenantId:       "tenant-1",
		CollectionName: "my-coll",
		Query:          "what is relevant?",
		TopK:           5,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Msg.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Msg.Results))
	}
	if resp.Msg.Results[0].ChunkText != "relevant content" {
		t.Fatalf("unexpected chunk text: %q", resp.Msg.Results[0].ChunkText)
	}
}

func TestQueryCollection_NoResults(t *testing.T) {
	srv, store := newTestServer()

	store.collections["empty-coll"] = &memory.Collection{
		ID:             "coll-1",
		Name:           "empty-coll",
		EmbeddingModel: "test-model",
	}
	store.queryResults = nil

	resp, err := srv.QueryCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.QueryCollectionRequest{
		TenantId:       "tenant-1",
		CollectionName: "empty-coll",
		Query:          "anything",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Msg.Results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(resp.Msg.Results))
	}
}

// ─── Edge Case Tests ────────────────────────────────────────────────

func TestRequireBackend_NilStore(t *testing.T) {
	srv := CreateServerWithDeps(context.Background(), nil, nil, "", 0)

	_, err := srv.CreateCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.CreateCollectionRequest{
		TenantId: "tenant-1",
		Name:     "test",
	}))
	if err == nil {
		t.Fatal("expected error for nil store")
	}

	connectErr := new(connect.Error)
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeUnavailable {
		t.Fatalf("expected CodeUnavailable, got %v", connectErr.Code())
	}
}

func TestCreateCollection_DefaultMetric(t *testing.T) {
	srv, store := newTestServer()

	resp, err := srv.CreateCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.CreateCollectionRequest{
		TenantId: "tenant-1",
		Name:     "default-metric",
		// distance_metric intentionally omitted
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	coll := store.collections["default-metric"]
	if coll == nil {
		t.Fatal("collection not created")
	}
	if coll.DistanceMetric != memory.DistanceCosine {
		t.Fatalf("expected cosine default, got %q", coll.DistanceMetric)
	}
	if resp.Msg.Collection.DistanceMetric != string(memory.DistanceCosine) {
		t.Fatalf("expected cosine in response, got %q", resp.Msg.Collection.DistanceMetric)
	}
}

func TestAddDocuments_CollectionNotFound(t *testing.T) {
	srv, _ := newTestServer()

	_, err := srv.AddDocuments(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.AddDocumentsRequest{
		TenantId:       "tenant-1",
		CollectionName: "nonexistent",
		Documents: []*memorypb.Document{
			{Content: "test content"},
		},
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent collection")
	}

	connectErr := new(connect.Error)
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %v", connectErr.Code())
	}
}

func TestGetMemoryAnalytics_NoDB(t *testing.T) {
	srv, _ := newTestServer()
	// DB is nil by default from newTestServer

	_, err := srv.GetMemoryAnalytics(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.GetMemoryAnalyticsRequest{
		TenantId: "tenant-1",
	}))
	if err == nil {
		t.Fatal("expected error for nil DB")
	}

	connectErr := new(connect.Error)
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeUnavailable {
		t.Fatalf("expected CodeUnavailable, got %v", connectErr.Code())
	}
}

func TestDeleteCollection_NotFound(t *testing.T) {
	srv, _ := newTestServer()

	_, err := srv.DeleteCollection(tenantCtx("tenant-1"), connect.NewRequest(&memorypb.DeleteCollectionRequest{
		TenantId: "tenant-1",
		Name:     "nonexistent",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent collection")
	}

	connectErr := new(connect.Error)
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound, got %v", connectErr.Code())
	}
}

// TestTenantIsolation_RejectsMissingContext exercises the regression that
// caused the cross-tenant data leak: every handler must refuse to run when
// the auth middleware did not set a tenant id, and must not fall back to a
// client-supplied value in the request body.
func TestTenantIsolation_RejectsMissingContext(t *testing.T) {
	srv, store := newTestServer()
	store.collections["a"] = &memory.Collection{ID: "1", Name: "a", TenantID: "tenant-a"}

	calls := []struct {
		name string
		fn   func() error
	}{
		{"ListCollections", func() error {
			_, err := srv.ListCollections(context.Background(), connect.NewRequest(&memorypb.ListCollectionsRequest{TenantId: "tenant-a"}))
			return err
		}},
		{"GetCollection", func() error {
			_, err := srv.GetCollection(context.Background(), connect.NewRequest(&memorypb.GetCollectionRequest{TenantId: "tenant-a", Name: "a"}))
			return err
		}},
		{"DeleteCollection", func() error {
			_, err := srv.DeleteCollection(context.Background(), connect.NewRequest(&memorypb.DeleteCollectionRequest{TenantId: "tenant-a", Name: "a"}))
			return err
		}},
		{"CreateCollection", func() error {
			_, err := srv.CreateCollection(context.Background(), connect.NewRequest(&memorypb.CreateCollectionRequest{TenantId: "tenant-a", Name: "x"}))
			return err
		}},
	}

	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			err := c.fn()
			if err == nil {
				t.Fatalf("%s: expected PermissionDenied when context lacks tenant id, got nil", c.name)
			}
			connectErr := new(connect.Error)
			if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
				t.Fatalf("%s: expected PermissionDenied, got %v", c.name, err)
			}
		})
	}
}

func TestSetupPgVector_NoDB(t *testing.T) {
	srv, _ := newTestServer()
	// DB is nil by default

	_, err := srv.SetupPgVector(context.Background(), connect.NewRequest(&memorypb.SetupPgVectorRequest{}))
	if err == nil {
		t.Fatal("expected error for nil DB")
	}

	connectErr := new(connect.Error)
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeUnavailable {
		t.Fatalf("expected CodeUnavailable, got %v", connectErr.Code())
	}
}
