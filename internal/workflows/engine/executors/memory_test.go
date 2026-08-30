package executors

import (
	"context"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// ─── Mock VectorStore ──────────────────────────────────────────────

type mockVectorStore struct {
	collections map[string]*memory.Collection
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
	return nil, context.DeadlineExceeded // Simulate not found
}

func (m *mockVectorStore) ListCollections(_ context.Context, _ string) ([]*memory.Collection, error) {
	var result []*memory.Collection
	for _, c := range m.collections {
		result = append(result, c)
	}
	return result, nil
}

func (m *mockVectorStore) DeleteCollection(_ context.Context, _, name string) error {
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

// ─── Helper to build messages, node + ec ───────────────────────────

func makeUserMessage(text string) gw.Message {
	t := text
	return gw.Message{
		Role: gw.RoleUser,
		Content: []gw.ContentPart{
			{Type: "text", Text: &t},
		},
	}
}

func makeMemoryNode(config map[string]interface{}) *engine.GraphNode {
	return &engine.GraphNode{
		ID:     "mem-1",
		Type:   "memory",
		Label:  "Memory",
		Config: config,
	}
}

func makeEC(userInput string) *engine.ExecutionContext {
	ec := engine.NewExecutionContext()
	if userInput != "" {
		ec.OriginalMessages = nil // force OriginalUserInput() to use Messages
		ec.Messages = append(ec.Messages, makeUserMessage(userInput))
	}
	return ec
}

// ─── Tests ─────────────────────────────────────────────────────────

func TestMemoryExecutor_Store_Success(t *testing.T) {
	store := newMockVectorStore()
	exec := &MemoryExecutor{
		Store:            store,
		Embedder:         &mockEmbedder{dimension: 128},
		DefaultModel:     "test-embed",
		DefaultDimension: 128,
	}

	node := makeMemoryNode(map[string]interface{}{
		"operation":      "store",
		"collection":     "test-coll",
		"contentSource":  "static",
		"staticContent":  "This is some test content to store in memory.",
		"chunkSize":      float64(512),
		"outputVariable": "my_result",
	})

	ec := makeEC("hello")

	result := exec.Execute(context.Background(), node, ec)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.NextHandle != "out" {
		t.Fatalf("expected handle 'out', got %q", result.NextHandle)
	}

	// Verify chunks were stored
	if len(store.storedChunks) == 0 {
		t.Fatal("expected chunks to be stored")
	}

	// Verify output variable was set
	v, ok := ec.Variables["my_result"]
	if !ok {
		t.Fatal("expected output variable 'my_result' to be set")
	}
	resultMap, ok := v.(map[string]interface{})
	if !ok {
		t.Fatal("expected output variable to be a map")
	}
	if resultMap["collection"] != "test-coll" {
		t.Fatalf("expected collection 'test-coll', got %v", resultMap["collection"])
	}
}

func TestMemoryExecutor_Query_Success(t *testing.T) {
	store := newMockVectorStore()
	store.collections["test-coll"] = &memory.Collection{
		ID:             "coll-test",
		Name:           "test-coll",
		EmbeddingModel: "test-embed",
	}
	store.queryResults = []memory.SearchResult{
		{DocumentID: "doc-1", ChunkText: "result chunk 1", Score: 0.95},
		{DocumentID: "doc-2", ChunkText: "result chunk 2", Score: 0.85},
	}

	exec := &MemoryExecutor{
		Store:            store,
		Embedder:         &mockEmbedder{dimension: 128},
		DefaultModel:     "test-embed",
		DefaultDimension: 128,
	}

	node := makeMemoryNode(map[string]interface{}{
		"operation":      "query",
		"collection":     "test-coll",
		"contentSource":  "input",
		"topK":           float64(5),
		"outputVariable": "query_results",
	})

	ec := makeEC("what is the meaning of life?")

	result := exec.Execute(context.Background(), node, ec)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.NextHandle != "out" {
		t.Fatalf("expected handle 'out', got %q", result.NextHandle)
	}

	// Verify output variables
	jsonResults, ok := ec.Variables["query_results"]
	if !ok {
		t.Fatal("expected query_results variable")
	}
	results, ok := jsonResults.([]map[string]interface{})
	if !ok {
		t.Fatal("expected query_results to be a slice of maps")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	textResults, ok := ec.Variables["query_results_text"]
	if !ok {
		t.Fatal("expected query_results_text variable")
	}
	if textResults.(string) == "" {
		t.Fatal("expected non-empty text results")
	}
}

func TestMemoryExecutor_NilBackend(t *testing.T) {
	exec := &MemoryExecutor{
		Store:    nil,
		Embedder: nil,
	}

	node := makeMemoryNode(map[string]interface{}{
		"operation": "query",
	})

	ec := makeEC("test")

	result := exec.Execute(context.Background(), node, ec)
	if result.Error == nil {
		t.Fatal("expected error for nil backend")
	}
	if result.Error.Error() != "memory backend not configured" {
		t.Fatalf("unexpected error message: %v", result.Error)
	}
}

func TestMemoryExecutor_UnknownOperation(t *testing.T) {
	store := newMockVectorStore()
	exec := &MemoryExecutor{
		Store:            store,
		Embedder:         &mockEmbedder{dimension: 128},
		DefaultModel:     "test-embed",
		DefaultDimension: 128,
	}

	node := makeMemoryNode(map[string]interface{}{
		"operation":    "delete_all",
		"collection":   "test-coll",
		"contentSource": "input",
	})

	ec := makeEC("test")

	result := exec.Execute(context.Background(), node, ec)
	if result.Error == nil {
		t.Fatal("expected error for unknown operation")
	}
}

func TestMemoryExecutor_EmptyContent(t *testing.T) {
	store := newMockVectorStore()
	exec := &MemoryExecutor{
		Store:            store,
		Embedder:         &mockEmbedder{dimension: 128},
		DefaultModel:     "test-embed",
		DefaultDimension: 128,
	}

	node := makeMemoryNode(map[string]interface{}{
		"operation":    "store",
		"collection":   "test-coll",
		"contentSource": "static",
		"staticContent": "",
	})

	ec := makeEC("") // No user input either

	result := exec.Execute(context.Background(), node, ec)
	if result.Error == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestResolveContentSource_Input(t *testing.T) {
	node := makeMemoryNode(map[string]interface{}{
		"contentSource": "input",
	})
	ec := makeEC("user question here")

	text := resolveContentSource(node, ec)
	if text != "user question here" {
		t.Fatalf("expected 'user question here', got %q", text)
	}
}

func TestResolveContentSource_Variable(t *testing.T) {
	node := makeMemoryNode(map[string]interface{}{
		"contentSource": "variable",
		"variableName":  "my_var",
	})
	ec := makeEC("")
	ec.SetVariable("my_var", "variable content")

	text := resolveContentSource(node, ec)
	if text != "variable content" {
		t.Fatalf("expected 'variable content', got %q", text)
	}
}

func TestResolveContentSource_Static(t *testing.T) {
	node := makeMemoryNode(map[string]interface{}{
		"contentSource": "static",
		"staticContent": "literal text",
	})
	ec := makeEC("")

	text := resolveContentSource(node, ec)
	if text != "literal text" {
		t.Fatalf("expected 'literal text', got %q", text)
	}
}

func TestResolveContentSource_Previous(t *testing.T) {
	node := makeMemoryNode(map[string]interface{}{
		"contentSource": "previous",
	})
	ec := makeEC("")
	ec.Ledger = engine.NewExecutionLedger()
	ec.Ledger.Record(&engine.NodeOutput{
		NodeID:   "prev-1",
		NodeType: "provider",
		Data: map[string]interface{}{
			"content": "previous node content",
		},
	})

	text := resolveContentSource(node, ec)
	if text != "previous node content" {
		t.Fatalf("expected 'previous node content', got %q", text)
	}
}

func TestResolveContentSource_Default(t *testing.T) {
	node := makeMemoryNode(map[string]interface{}{})
	ec := makeEC("default input")

	text := resolveContentSource(node, ec)
	if text != "default input" {
		t.Fatalf("expected 'default input', got %q", text)
	}
}
