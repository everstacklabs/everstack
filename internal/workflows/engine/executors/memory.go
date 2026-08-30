package executors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// MemoryExecutor handles vector memory store/query operations within a workflow.
//
// Config fields (from frontend MemoryConfig):
//   - operation: "store" | "query"
//   - collection: collection name (default: "default")
//   - contentSource: "input" | "variable" | "previous" | "static"
//   - variableName: variable name when contentSource = "variable"
//   - staticContent: literal text when contentSource = "static"
//   - source: document source label for store operations
//   - chunkSize: max chunk size in characters (default: 512)
//   - topK: number of results for query (default: 5)
//   - minScore: minimum similarity threshold (default: 0.0)
//   - embeddingModel: override embedding model (blank = system default)
//   - outputVariable: variable name to store results (default: "memory_results")
type MemoryExecutor struct {
	Store            memory.VectorStore
	Embedder         memory.EmbedderInterface
	DefaultModel     string
	DefaultDimension int
}

func (e *MemoryExecutor) NodeType() string { return "memory" }

func (e *MemoryExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	if e.Store == nil || e.Embedder == nil {
		return engine.NodeResult{Error: fmt.Errorf("memory backend not configured")}
	}

	operation := node.GetConfigString("operation")
	if operation == "" {
		operation = "query"
	}

	collection := node.GetConfigString("collection")
	if collection == "" {
		collection = "default"
	}

	tenantID := contextkeys.GetTenantID(ctx)

	// Resolve text from configured content source
	text := resolveContentSource(node, ec)
	if text == "" {
		return engine.NodeResult{Error: fmt.Errorf("memory: no content to %s", operation)}
	}

	// Resolve embedding model
	embModel := node.GetConfigString("embeddingModel")
	if embModel == "" {
		embModel = e.DefaultModel
	}
	embDim := e.DefaultDimension

	// Ensure collection exists (get or create)
	coll, err := e.Store.GetCollection(ctx, tenantID, collection)
	if err != nil || coll == nil {
		coll, err = e.Store.CreateCollection(ctx, tenantID, memory.CollectionOptions{
			Name:               collection,
			EmbeddingModel:     embModel,
			EmbeddingDimension: embDim,
			DistanceMetric:     memory.DistanceCosine,
		})
		if err != nil {
			return engine.NodeResult{Error: fmt.Errorf("memory: create collection: %w", err)}
		}
	}

	logger.WithFields(
		"operation", operation,
		"collection", collection,
		"tenant_id", tenantID,
		"text_len", len(text),
	).Debug("memory executor: executing")

	switch operation {
	case "store":
		return e.executeStore(ctx, node, ec, coll, text, embModel)
	case "query":
		return e.executeQuery(ctx, node, ec, coll, text, embModel)
	default:
		return engine.NodeResult{Error: fmt.Errorf("memory: unknown operation %q", operation)}
	}
}

// executeStore chunks the text, generates embeddings, and stores them.
func (e *MemoryExecutor) executeStore(
	ctx context.Context,
	node *engine.GraphNode,
	ec *engine.ExecutionContext,
	coll *memory.Collection,
	text, embModel string,
) engine.NodeResult {
	chunkSize := node.GetConfigInt("chunkSize")
	if chunkSize <= 0 {
		chunkSize = 512
	}
	source := node.GetConfigString("source")
	outputVar := node.GetConfigString("outputVariable")
	if outputVar == "" {
		outputVar = "memory_results"
	}

	// Chunk text
	chunks := memory.ChunkText(text, chunkSize)

	// Generate embeddings
	embeddings, err := e.Embedder.EmbedBatch(ctx, embModel, chunks)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("memory store: embedding failed: %w", err)}
	}

	// Build chunk objects
	memChunks := make([]memory.Chunk, len(chunks))
	for i, chunkText := range chunks {
		meta := map[string]string{}
		if source != "" {
			meta["source"] = source
		}
		memChunks[i] = memory.Chunk{
			Text:       chunkText,
			ChunkIndex: i,
			Embedding:  embeddings[i],
			Metadata:   meta,
		}
	}

	// Store
	if err := e.Store.Store(ctx, coll.ID, memChunks); err != nil {
		return engine.NodeResult{Error: fmt.Errorf("memory store: %w", err)}
	}

	logger.WithFields(
		"collection", coll.Name,
		"chunks", len(memChunks),
	).Debug("memory executor: stored chunks")

	// Set execution context variables
	storeResult := map[string]interface{}{
		"collection":  coll.Name,
		"chunk_count": len(memChunks),
	}
	ec.SetVariable(outputVar, storeResult)

	// Node data for execution events
	ec.SetNodeData("operation", "store")
	ec.SetNodeData("collection", coll.Name)
	ec.SetNodeData("chunks_stored", fmt.Sprintf("%d", len(memChunks)))

	return engine.NodeResult{
		NextHandle: "out",
		Output: map[string]interface{}{
			"operation":   "store",
			"collection":  coll.Name,
			"chunk_count": len(memChunks),
		},
	}
}

// executeQuery embeds the query text and searches the collection.
func (e *MemoryExecutor) executeQuery(
	ctx context.Context,
	node *engine.GraphNode,
	ec *engine.ExecutionContext,
	coll *memory.Collection,
	text, embModel string,
) engine.NodeResult {
	topK := node.GetConfigInt("topK")
	if topK <= 0 {
		topK = 5
	}
	minScore := node.GetConfigFloat("minScore")
	outputVar := node.GetConfigString("outputVariable")
	if outputVar == "" {
		outputVar = "memory_results"
	}

	// Generate query embedding
	embedding, err := e.Embedder.Embed(ctx, embModel, text)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("memory query: embedding failed: %w", err)}
	}

	// Search
	results, err := e.Store.Query(ctx, coll.ID, embedding, memory.QueryOptions{
		TopK:     topK,
		MinScore: float32(minScore),
	})
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("memory query: %w", err)}
	}

	logger.WithFields(
		"collection", coll.Name,
		"results", len(results),
		"top_k", topK,
	).Debug("memory executor: query completed")

	// Format results as text for downstream context injection
	var resultsText strings.Builder
	for i, r := range results {
		resultsText.WriteString(fmt.Sprintf("[%d] (score: %.3f) %s\n", i+1, r.Score, r.ChunkText))
	}

	// Serialize results for variable storage
	resultsJSON := make([]map[string]interface{}, len(results))
	for i, r := range results {
		resultsJSON[i] = map[string]interface{}{
			"chunk_text":  r.ChunkText,
			"score":       r.Score,
			"document_id": r.DocumentID,
			"chunk_index": r.ChunkIndex,
			"metadata":    r.Metadata,
		}
	}

	// Set execution context variables
	ec.SetVariable(outputVar, resultsJSON)
	ec.SetVariable(outputVar+"_text", resultsText.String())

	// Node data for execution events
	ec.SetNodeData("operation", "query")
	ec.SetNodeData("collection", coll.Name)
	ec.SetNodeData("results_count", fmt.Sprintf("%d", len(results)))

	return engine.NodeResult{
		NextHandle: "out",
		Output: map[string]interface{}{
			"operation":    "query",
			"collection":   coll.Name,
			"results_count": len(results),
			"results_text": resultsText.String(),
		},
	}
}

// resolveContentSource extracts the text content from the configured source.
func resolveContentSource(node *engine.GraphNode, ec *engine.ExecutionContext) string {
	switch node.GetConfigString("contentSource") {
	case "input":
		return ec.OriginalUserInput()

	case "variable":
		varName := node.GetConfigString("variableName")
		if varName == "" {
			return ""
		}
		return ec.GetStringVariable(varName)

	case "previous":
		if ec.Ledger == nil {
			return ""
		}
		prev := ec.Ledger.Previous()
		if prev == nil {
			return ""
		}
		// Try common data keys
		if v := prev.GetString("content"); v != "" {
			return v
		}
		if v := prev.GetString("body"); v != "" {
			return v
		}
		// Fall back to JSON-serialized output
		if prev.Data != nil {
			b, err := json.Marshal(prev.Data)
			if err == nil {
				return string(b)
			}
		}
		return ""

	case "static":
		text := node.GetConfigString("staticContent")
		if text != "" && ec.Ledger != nil {
			text = ec.Ledger.InterpolateTemplate(text, ec)
		}
		return text

	default:
		// Default to user input
		return ec.OriginalUserInput()
	}
}
