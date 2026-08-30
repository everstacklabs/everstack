package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/memory"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// MemoryQueryHandler handles the memory_query synthetic tool.
type MemoryQueryHandler struct {
	Store    memory.VectorStore
	Embedder memory.EmbedderInterface
	TenantID string
}

func (h *MemoryQueryHandler) Name() string { return "memory_query" }

func (h *MemoryQueryHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "memory_query",
			Description: "Search long-term vector memory for relevant information. Returns the most similar stored content based on semantic similarity to the query.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"collection": map[string]interface{}{
						"type":        "string",
						"description": "Name of the memory collection to search in.",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The search query. Will be semantically matched against stored content.",
					},
					"top_k": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results to return (default: 5).",
					},
					"min_score": map[string]interface{}{
						"type":        "number",
						"description": "Minimum similarity score threshold (0-1, default: 0).",
					},
				},
				"required": []string{"collection", "query"},
			},
		},
	}
}

func (h *MemoryQueryHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	collectionName, _ := args["collection"].(string)
	queryText, _ := args["query"].(string)

	if collectionName == "" || queryText == "" {
		return "", fmt.Errorf("collection and query are required")
	}

	topK := 5
	if k, ok := args["top_k"].(float64); ok && k > 0 {
		topK = int(k)
	}

	var minScore float32
	if s, ok := args["min_score"].(float64); ok {
		minScore = float32(s)
	}

	// Get collection
	collection, err := h.Store.GetCollection(ctx, h.TenantID, collectionName)
	if err != nil {
		return fmt.Sprintf("Collection %q not found. No memories stored in this collection yet.", collectionName), nil
	}

	// Generate query embedding
	embedding, err := h.Embedder.Embed(ctx, collection.EmbeddingModel, queryText)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search
	results, err := h.Store.Query(ctx, collection.ID, embedding, memory.QueryOptions{
		TopK:     topK,
		MinScore: minScore,
	})
	if err != nil {
		return "", fmt.Errorf("memory query failed: %w", err)
	}

	if len(results) == 0 {
		return fmt.Sprintf("No relevant memories found in collection %q for query: %s", collectionName, queryText), nil
	}

	// Format results
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant memories in collection %q:\n\n", len(results), collectionName))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("--- Result %d (score: %.4f) ---\n", i+1, r.Score))
		sb.WriteString(r.ChunkText)
		sb.WriteString("\n")
		if len(r.Metadata) > 0 {
			sb.WriteString("Metadata: ")
			parts := make([]string, 0, len(r.Metadata))
			for k, v := range r.Metadata {
				parts = append(parts, fmt.Sprintf("%s=%s", k, v))
			}
			sb.WriteString(strings.Join(parts, ", "))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
