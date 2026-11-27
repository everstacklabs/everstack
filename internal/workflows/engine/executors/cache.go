package executors

import (
	"context"
	"encoding/json"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cache"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// CacheExecutor handles cache lookup nodes in the workflow.
//
// Config fields (from frontend CacheConfig):
//   - type: "exact" | "semantic" (default: "exact")
//   - ttl: time-to-live in seconds (default: 300)
//   - maxEntries: max cache entries (default: 1000)
//   - similarityThreshold: threshold for semantic matching (default: 0.8)
//
// The executor delegates to the global fastpath engine for cache lookups,
// using the same infrastructure as the gateway pipeline.
//
// Handles: "hit" when cached response found, "miss" when not found.
type CacheExecutor struct{}

func (e *CacheExecutor) NodeType() string { return "cache" }

func (e *CacheExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	cacheType := node.GetConfigString("type")
	if cacheType == "" {
		cacheType = "exact"
	}

	// Extract query from the last user message
	query := extractUserQuery(ec)
	if query == "" {
		ec.CacheHit = false
		logger.Debug("cache executor: no user query found, treating as miss")
		return engine.NodeResult{NextHandle: "miss", Output: map[string]interface{}{"hit": false, "type": cacheType}}
	}

	// Store cache metadata for post-provider cache write
	ec.CacheType = cacheType
	ec.CacheQuery = query
	ec.SetNodeData("cache_type", cacheType)
	queryPreview := query
	if len(queryPreview) > 200 {
		queryPreview = queryPreview[:200]
	}
	ec.SetNodeData("cache_query", queryPreview)

	fpEngine := fastpath.GetGlobalEngine()
	if fpEngine == nil || !fpEngine.IsEnabled() {
		ec.CacheHit = false
		ec.CacheMiss = true
		logger.Debug("cache executor: fastpath engine not available, treating as miss")
		return engine.NodeResult{NextHandle: "miss", Output: map[string]interface{}{"hit": false, "type": cacheType}}
	}

	// Snapshot the key request now: the provider node rewrites ec.Messages
	// before the post-provider cache write, which must reuse this snapshot.
	keyReq := engine.CacheKeyRequest(ec.ResolvedModel, ec.Messages, ec.SamplingParams)
	ec.CacheKey = &keyReq
	req := engine.NewCacheRequest(keyReq)

	switch cacheType {
	case "semantic":
		return e.lookupSemantic(ctx, fpEngine, ec, req, query, cacheType)
	default:
		return e.lookupExact(ctx, fpEngine, ec, req, query, cacheType)
	}
}

func (e *CacheExecutor) lookupExact(ctx context.Context, fpEngine *fastpath.Engine, ec *engine.ExecutionContext, req *engine.CacheRequest, query string, cacheType string) engine.NodeResult {
	cached, ok := fpEngine.GetCachedResponseWithContext(ctx, req)
	if !ok {
		ec.CacheHit = false
		ec.CacheMiss = true
		ec.SetNodeData("cache_result", "miss")
		logger.WithFields("query_len", len(query)).Debug("cache executor: exact cache miss")
		return engine.NodeResult{NextHandle: "miss", Output: map[string]interface{}{"hit": false, "type": cacheType}}
	}

	resp, err := deserializeCachedResponse(cached)
	if err != nil {
		ec.CacheHit = false
		ec.CacheMiss = true
		ec.SetNodeData("cache_result", "miss")
		logger.WithFields("error", err.Error()).Warn("cache executor: failed to deserialize cached response")
		return engine.NodeResult{NextHandle: "miss", Output: map[string]interface{}{"hit": false, "type": cacheType}}
	}

	ec.CacheHit = true
	ec.CacheMiss = false
	ec.Response = resp
	ec.SetNodeData("cache_type", "exact")
	ec.SetNodeData("cache_result", "hit")
	logger.WithFields("model", cached.Model).Debug("cache executor: exact cache hit")
	return engine.NodeResult{NextHandle: "hit", Output: map[string]interface{}{"hit": true, "type": cacheType}}
}

func (e *CacheExecutor) lookupSemantic(ctx context.Context, fpEngine *fastpath.Engine, ec *engine.ExecutionContext, req *engine.CacheRequest, query string, cacheType string) engine.NodeResult {
	if !fpEngine.IsSemanticCacheEnabled() {
		ec.CacheHit = false
		ec.CacheMiss = true
		ec.SetNodeData("cache_result", "miss")
		logger.Debug("cache executor: semantic cache not enabled, treating as miss")
		return engine.NodeResult{NextHandle: "miss", Output: map[string]interface{}{"hit": false, "type": cacheType}}
	}

	cached, ok := fpEngine.GetSemanticCachedResponseWithContext(ctx, req, query)
	if !ok {
		ec.CacheHit = false
		ec.CacheMiss = true
		ec.SetNodeData("cache_result", "miss")
		logger.WithFields("query_len", len(query)).Debug("cache executor: semantic cache miss")
		return engine.NodeResult{NextHandle: "miss", Output: map[string]interface{}{"hit": false, "type": cacheType}}
	}

	resp, err := deserializeCachedResponse(cached)
	if err != nil {
		ec.CacheHit = false
		ec.CacheMiss = true
		ec.SetNodeData("cache_result", "miss")
		logger.WithFields("error", err.Error()).Warn("cache executor: failed to deserialize cached response")
		return engine.NodeResult{NextHandle: "miss", Output: map[string]interface{}{"hit": false, "type": cacheType}}
	}

	ec.CacheHit = true
	ec.CacheMiss = false
	ec.Response = resp
	ec.SetNodeData("cache_type", "semantic")
	ec.SetNodeData("cache_result", "hit")
	logger.WithFields("model", cached.Model).Debug("cache executor: semantic cache hit")
	return engine.NodeResult{NextHandle: "hit", Output: map[string]interface{}{"hit": true, "type": cacheType}}
}

// extractUserQuery returns the text content of the last user message.
func extractUserQuery(ec *engine.ExecutionContext) string {
	for i := len(ec.Messages) - 1; i >= 0; i-- {
		if ec.Messages[i].Role == gw.RoleUser && len(ec.Messages[i].Content) > 0 {
			if ec.Messages[i].Content[0].Text != nil {
				return *ec.Messages[i].Content[0].Text
			}
		}
	}
	return ""
}

// deserializeCachedResponse converts a CachedResponse back into a ChatCompletionResponse.
func deserializeCachedResponse(cached *cache.CachedResponse) (*gw.ChatCompletionResponse, error) {
	if cached == nil || len(cached.Response) == 0 {
		return nil, nil
	}

	var resp gw.ChatCompletionResponse
	if err := json.Unmarshal(cached.Response, &resp); err != nil {
		return nil, err
	}

	// Restore model from cache metadata if response model is empty
	if resp.Model == "" && cached.Model != "" {
		resp.Model = cached.Model
	}

	// Restore usage from cache metadata
	if resp.Usage.PromptTokens == 0 && cached.InputTokens > 0 {
		resp.Usage.PromptTokens = cached.InputTokens
	}
	if resp.Usage.CompletionTokens == 0 && cached.OutputTokens > 0 {
		resp.Usage.CompletionTokens = cached.OutputTokens
	}
	if resp.Usage.TotalTokens == 0 {
		resp.Usage.TotalTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	}

	if resp.Created.IsZero() {
		resp.Created = time.Now()
	}

	return &resp, nil
}
