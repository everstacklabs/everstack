package eval_runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/memory"
	"github.com/everstacklabs/everstack/internal/query"
	traceshandler "github.com/everstacklabs/everstack/internal/query/handlers/traces"
)

// RetrievalConfig controls what context is fetched for scoring.
type RetrievalConfig struct {
	Enabled             bool     `json:"enabled"`
	VectorCollections   []string `json:"vector_collections"`
	EmbeddingModel      string   `json:"embedding_model"`
	TopK                int      `json:"top_k"`
	IncludeTraceContext bool     `json:"include_trace_context"`
}

// EvalRetriever assembles RAG context from existing data sources for the scoring pipeline.
type EvalRetriever struct {
	chConn      clickhouse.Conn
	vectorStore memory.VectorStore
	embedder    memory.EmbedderInterface
}

// NewEvalRetriever creates a new retriever. vectorStore and embedder may be nil
// if only trace-based retrieval is needed.
func NewEvalRetriever(chConn clickhouse.Conn, vectorStore memory.VectorStore, embedder memory.EmbedderInterface) *EvalRetriever {
	return &EvalRetriever{
		chConn:      chConn,
		vectorStore: vectorStore,
		embedder:    embedder,
	}
}

// Retrieve assembles context from trace data, vector store, and dataset metadata.
// Returns a single concatenated context string.
func (r *EvalRetriever) Retrieve(ctx context.Context, item pendingItemRow, cfg RetrievalConfig) (string, error) {
	var parts []string

	// 1. Trace context
	if cfg.IncludeTraceContext && item.SourceTraceID != "" && r.chConn != nil {
		traceCtx, err := r.getTraceContext(ctx, item.SourceTraceID)
		if err != nil {
			logger.WithFields("trace_id", item.SourceTraceID).WithError(err).Warn("eval_retriever: trace context fetch failed")
		} else if traceCtx != "" {
			parts = append(parts, "--- Trace Context ---\n"+traceCtx)
		}
	}

	// 2. Vector store retrieval
	if len(cfg.VectorCollections) > 0 && r.vectorStore != nil && r.embedder != nil {
		inputText := extractInputText(item.Input)
		if inputText != "" {
			model := cfg.EmbeddingModel
			if model == "" {
				model = "text-embedding-3-small"
			}
			topK := cfg.TopK
			if topK <= 0 {
				topK = 5
			}
			vectorCtx, err := r.queryVectorStore(ctx, inputText, model, cfg.VectorCollections, topK)
			if err != nil {
				logger.WithError(err).Warn("eval_retriever: vector store query failed")
			} else if vectorCtx != "" {
				parts = append(parts, "--- Retrieved Context ---\n"+vectorCtx)
			}
		}
	}

	// 3. Dataset metadata
	if len(item.Metadata) > 0 {
		metaCtx := extractMetadataContext(item.Metadata)
		if metaCtx != "" {
			parts = append(parts, "--- Dataset Metadata ---\n"+metaCtx)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

func (r *EvalRetriever) getTraceContext(ctx context.Context, traceID string) (string, error) {
	handler := traceshandler.NewGetTraceHandler(r.chConn)
	q := traceshandler.NewGetTraceQuery(traceID, "", "")
	res, err := handler.Handle(ctx, q)
	if err != nil {
		return "", err
	}
	trace, ok := res.(*query.TraceReadModel)
	if !ok || trace == nil {
		return "", fmt.Errorf("trace not found")
	}

	var sb strings.Builder
	if trace.TraceInput != "" {
		sb.WriteString("Input: ")
		sb.WriteString(trace.TraceInput)
		sb.WriteString("\n")
	}
	if trace.TraceOutput != "" {
		sb.WriteString("Output: ")
		sb.WriteString(trace.TraceOutput)
		sb.WriteString("\n")
	}
	if trace.SpanCount > 0 {
		sb.WriteString(fmt.Sprintf("Spans: %d\n", trace.SpanCount))
	}
	if trace.RequestedModel != "" {
		sb.WriteString(fmt.Sprintf("Model: %s\n", trace.RequestedModel))
	}
	return sb.String(), nil
}

func (r *EvalRetriever) queryVectorStore(ctx context.Context, inputText, model string, collections []string, topK int) (string, error) {
	embedding, err := r.embedder.Embed(ctx, model, inputText)
	if err != nil {
		return "", fmt.Errorf("embedding failed: %w", err)
	}

	var results []string
	for _, collID := range collections {
		searchResults, err := r.vectorStore.Query(ctx, collID, embedding, memory.QueryOptions{
			TopK: topK,
		})
		if err != nil {
			logger.WithFields("collection", collID).WithError(err).Warn("eval_retriever: collection query failed")
			continue
		}
		for _, sr := range searchResults {
			if sr.ChunkText != "" {
				results = append(results, sr.ChunkText)
			}
		}
	}

	return strings.Join(results, "\n---\n"), nil
}

func extractInputText(inputRaw []byte) string {
	if len(inputRaw) == 0 {
		return ""
	}
	var parsed interface{}
	if err := json.Unmarshal(inputRaw, &parsed); err != nil {
		return string(inputRaw)
	}

	switch v := parsed.(type) {
	case string:
		return v
	case map[string]interface{}:
		// Try common field names
		for _, key := range []string{"content", "text", "prompt", "message", "input", "query"} {
			if s, ok := v[key].(string); ok && s != "" {
				return s
			}
		}
		// Fall back to JSON serialization
		b, _ := json.Marshal(v)
		return string(b)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func extractMetadataContext(metadataRaw []byte) string {
	if len(metadataRaw) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(metadataRaw, &m); err != nil {
		return ""
	}
	if len(m) == 0 {
		return ""
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b)
}

// parseRetrievalConfig extracts RetrievalConfig from eval_config JSONB.
func parseRetrievalConfig(evalConfig []byte) RetrievalConfig {
	var cfg RetrievalConfig
	if len(evalConfig) == 0 {
		return cfg
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(evalConfig, &raw); err != nil {
		return cfg
	}
	retrieval, ok := raw["retrieval"]
	if !ok {
		return cfg
	}
	b, err := json.Marshal(retrieval)
	if err != nil {
		return cfg
	}
	json.Unmarshal(b, &cfg)
	return cfg
}
