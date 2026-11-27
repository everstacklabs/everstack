package executors

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// fakeProviderExecutor mutates ec.Messages the way ProviderExecutor does: it
// inserts an upstream-context system message before the last user turn (in
// place, sharing the backing array) and appends the assistant reply.
type fakeProviderExecutor struct{ calls atomic.Int32 }

func (f *fakeProviderExecutor) NodeType() string { return "provider" }

func (f *fakeProviderExecutor) Execute(_ context.Context, _ *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	f.calls.Add(1)
	ctxText := "upstream node output"
	ctxMsg := gw.Message{Role: gw.RoleSystem, Content: []gw.ContentPart{{Type: "text", Text: &ctxText}}}
	for i := len(ec.Messages) - 1; i >= 0; i-- {
		if ec.Messages[i].Role == gw.RoleUser {
			ec.Messages = append(ec.Messages[:i], append([]gw.Message{ctxMsg}, ec.Messages[i:]...)...)
			break
		}
	}
	answer := "provider answer"
	msg := gw.Message{Role: gw.RoleAssistant, Content: []gw.ContentPart{{Type: "text", Text: &answer}}}
	ec.Response = &gw.ChatCompletionResponse{ID: "r1", Model: "gpt-4o-mini", Choices: []gw.Choice{{Message: msg}}}
	ec.Messages = append(ec.Messages, msg)
	return engine.NodeResult{NextHandle: "out"}
}

func cacheWorkflowGraph(cacheType string) *engine.Graph {
	cacheNode := &engine.GraphNode{ID: "cache", Type: "cache", Config: map[string]interface{}{"type": cacheType}}
	providerNode := &engine.GraphNode{ID: "llm", Type: "provider"}
	miss := &engine.GraphEdge{ID: "e1", Source: "cache", Target: "llm", SourceHandle: "miss"}
	cacheNode.OutEdges = []*engine.GraphEdge{miss}
	providerNode.InEdges = []*engine.GraphEdge{miss}
	return &engine.Graph{
		Nodes:   map[string]*engine.GraphNode{"cache": cacheNode, "llm": providerNode},
		Edges:   []*engine.GraphEdge{miss},
		StartID: "cache",
	}
}

func newCacheRunContext(tenant string) *engine.ExecutionContext {
	ec := engine.NewExecutionContext()
	ec.WorkflowID = "wf-1"
	ec.TenantID = tenant
	ec.ResolvedModel = "gpt-4o-mini"
	q := "What is the refund policy?"
	// Spare capacity so an in-place insert would corrupt a shallow snapshot.
	msgs := make([]gw.Message, 1, 8)
	msgs[0] = gw.Message{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: &q}}}
	ec.Messages = msgs
	return ec
}

// The cache node's lookup and the post-provider write must use the same key:
// a second identical run hits; another tenant's identical run misses.
func TestWorkflowCacheRoundTrip(t *testing.T) {
	features := validator.DefaultFastPathFeaturesConfig()
	features.Enabled = true
	features.Cache.Exact.Enabled = true
	features.Cache.Semantic.Enabled = true

	for _, cacheType := range []string{"exact", "semantic"} {
		t.Run(cacheType, func(t *testing.T) {
			fp, err := fastpath.NewEngine(fastpath.EngineConfig{Features: features})
			if err != nil {
				t.Fatal(err)
			}
			prev := fastpath.GetGlobalEngine()
			fastpath.SetGlobalEngine(fp)
			t.Cleanup(func() { fastpath.SetGlobalEngine(prev) })

			provider := &fakeProviderExecutor{}
			eng := engine.NewEngine(&engine.EngineDeps{})
			eng.RegisterExecutor(&CacheExecutor{})
			eng.RegisterExecutor(provider)

			run := func(tenant string) *engine.ExecutionContext {
				ctx := contextkeys.WithTenantID(context.Background(), tenant)
				ec := newCacheRunContext(tenant)
				if err := eng.Execute(ctx, cacheWorkflowGraph(cacheType), ec); err != nil {
					t.Fatalf("execute: %v", err)
				}
				return ec
			}

			if ec := run("tenant-a"); ec.CacheHit {
				t.Fatal("first run should miss")
			}
			time.Sleep(200 * time.Millisecond) // async cache write

			if ec := run("tenant-a"); !ec.CacheHit {
				t.Fatalf("second identical run by the same tenant missed (provider calls=%d)", provider.calls.Load())
			}
			if ec := run("tenant-b"); ec.CacheHit {
				t.Fatal("another tenant's identical run hit tenant A's entry")
			}
		})
	}
}
