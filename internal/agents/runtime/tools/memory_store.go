package tools

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// MemoryStoreHandler handles the memory_store synthetic tool.
type MemoryStoreHandler struct {
	Store                     memory.VectorStore
	Embedder                  memory.EmbedderInterface
	TenantID                  string
	DefaultEmbeddingModel     string
	DefaultEmbeddingDimension int
}

func (h *MemoryStoreHandler) Name() string { return "memory_store" }

func (h *MemoryStoreHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "memory_store",
			Description: "Store information in long-term vector memory for later retrieval. Use this to remember important facts, decisions, or context that may be useful in future interactions.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{
						"type":        "string",
						"description": "Name of the memory collection to store in. Will be created if it doesn't exist.",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The content to store in memory.",
					},
					"source": map[string]interface{}{
						"type":        "string",
						"description": "Optional source identifier for the content.",
					},
					"metadata": map[string]interface{}{
						"type":        "object",
						"description": "Optional key-value metadata to attach to the stored content.",
						"additionalProperties": map[string]interface{}{
							"type": "string",
						},
					},
				},
				"required": []string{"collection", "content"},
			},
		},
	}
}

func (h *MemoryStoreHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	collectionName, _ := args["collection"].(string)
	content, _ := args["content"].(string)
	source, _ := args["source"].(string)

	if collectionName == "" || content == "" {
		return "", fmt.Errorf("collection and content are required")
	}

	var metadata map[string]string
	if m, ok := args["metadata"].(map[string]interface{}); ok {
		metadata = make(map[string]string)
		for k, v := range m {
			if s, ok := v.(string); ok {
				metadata[k] = s
			}
		}
	}

	embeddingModel := h.DefaultEmbeddingModel
	if embeddingModel == "" {
		return "", fmt.Errorf("no embedding model configured — set memory.embedding_model in gateway features config")
	}

	embeddingDimension := h.DefaultEmbeddingDimension
	if embeddingDimension <= 0 {
		return "", fmt.Errorf("no embedding dimension configured — set memory.embedding_dimension in gateway features config")
	}

	// Get or create collection
	collection, err := h.Store.GetCollection(ctx, h.TenantID, collectionName)
	if err != nil {
		// Create collection
		collection, err = h.Store.CreateCollection(ctx, h.TenantID, memory.CollectionOptions{
			Name:               collectionName,
			EmbeddingModel:     embeddingModel,
			EmbeddingDimension: embeddingDimension,
			DistanceMetric:     memory.DistanceCosine,
		})
		if err != nil {
			return "", fmt.Errorf("failed to create collection: %w", err)
		}
	}

	// Add document
	docIDs, err := h.Store.AddDocuments(ctx, collection.ID, []memory.Document{
		{Content: content, Metadata: metadata, Source: source},
	})
	if err != nil {
		return "", fmt.Errorf("failed to add document: %w", err)
	}

	if len(docIDs) == 0 {
		return "", fmt.Errorf("no document ID returned")
	}

	// Chunk and embed
	chunks := memory.ChunkText(content, 512)
	embeddingChunks := make([]memory.Chunk, 0, len(chunks))

	for i, chunkText := range chunks {
		embedding, err := h.Embedder.Embed(ctx, collection.EmbeddingModel, chunkText)
		if err != nil {
			logger.WithFields("error", err.Error(), "chunk_index", i).
				Warn("memory_store: failed to generate embedding for chunk")
			continue
		}

		embeddingChunks = append(embeddingChunks, memory.Chunk{
			DocumentID: docIDs[0],
			Text:       chunkText,
			ChunkIndex: i,
			Embedding:  embedding,
			Metadata:   metadata,
		})
	}

	if len(embeddingChunks) > 0 {
		if err := h.Store.Store(ctx, collection.ID, embeddingChunks); err != nil {
			return "", fmt.Errorf("failed to store embeddings: %w", err)
		}
	}

	return fmt.Sprintf("Successfully stored %d chunk(s) in collection %q (document ID: %s)", len(embeddingChunks), collectionName, docIDs[0]), nil
}
