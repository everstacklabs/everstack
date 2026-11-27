package cacheexec

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
)

// newScopedTestEngine builds one shared engine (exact + MinHash semantic),
// the same shape a shared multi-tenant gateway runs with fastpath enabled.
func newScopedTestEngine(t *testing.T) *fastpath.Engine {
	t.Helper()
	features := validator.DefaultFastPathFeaturesConfig()
	features.Enabled = true
	features.Cache.Exact.Enabled = true
	features.Cache.Semantic.Enabled = true
	engine, err := fastpath.NewEngine(fastpath.EngineConfig{Features: features})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

func tenantCtx(engine *fastpath.Engine, tenantID string) context.Context {
	ctx := fastpath.WithEngine(context.Background(), engine)
	if tenantID != "" {
		ctx = contextkeys.WithTenantID(ctx, tenantID)
	}
	return ctx
}

func textPtr(s string) *string { return &s }

func chatReq(system, user string) gw.ChatCompletionRequest {
	msgs := []gw.Message{}
	if system != "" {
		msgs = append(msgs, gw.Message{Role: gw.RoleSystem, Content: []gw.ContentPart{{Type: "text", Text: textPtr(system)}}})
	}
	msgs = append(msgs, gw.Message{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "text", Text: textPtr(user)}}})
	return gw.ChatCompletionRequest{Model: "gpt-4o-mini", Messages: msgs}
}

func chatAnswer(text string) gw.ChatCompletionResponse {
	return gw.ChatCompletionResponse{
		ID:      "chatcmpl-" + text,
		Model:   "gpt-4o-mini",
		Choices: []gw.Choice{{Message: gw.Message{Role: gw.RoleAssistant, Content: []gw.ContentPart{{Type: "text", Text: textPtr(text)}}}}},
	}
}

func responsesReq(instructions, user string) gw.CreateResponseRequest {
	return gw.CreateResponseRequest{
		Model:        "gpt-4o-mini",
		Instructions: instructions,
		Input: []gw.ResponseInput{{
			Type:    "message",
			Role:    gw.RoleUser,
			Content: []gw.ContentPart{{Type: "text", Text: textPtr(user)}},
		}},
	}
}

func responsesAnswer(text string) gw.CreateResponseResponse {
	return gw.CreateResponseResponse{
		ID:     "resp-" + text,
		Status: "completed",
		Model:  "gpt-4o-mini",
		Output: []gw.ResponseOutputItem{{Type: "message", Role: gw.RoleAssistant, Content: []gw.ContentPart{{Type: "text", Text: textPtr(text)}}}},
	}
}

// waitSemanticStore gives the asynchronous semantic store time to land.
func waitSemanticStore() { time.Sleep(150 * time.Millisecond) }

func TestChatCacheDoesNotServeAnotherTenantsCompletion(t *testing.T) {
	engine := newScopedTestEngine(t)
	req := chatReq("", "What is our Q3 revenue figure?")

	_, outA, err := ExecuteChatWithCacheOutcome(tenantCtx(engine, "tenant-a"), req,
		func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			return chatAnswer("tenant A private answer"), nil
		})
	if err != nil || !outA.Stored {
		t.Fatalf("tenant A store failed: err=%v outcome=%+v", err, outA)
	}
	waitSemanticStore()

	var providerCalls atomic.Int32
	respB, outB, err := ExecuteChatWithCacheOutcome(tenantCtx(engine, "tenant-b"), req,
		func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			providerCalls.Add(1)
			return chatAnswer("tenant B own answer"), nil
		})
	if err != nil {
		t.Fatalf("tenant B: %v", err)
	}
	if outB.Hit || providerCalls.Load() != 1 {
		t.Fatalf("tenant B was served tenant A's cached completion: hit=%v type=%q body=%q",
			outB.Hit, outB.HitType, *respB.Choices[0].Message.Content[0].Text)
	}

	// The owning tenant still gets its own hit: scoping must not disable caching.
	_, outA2, err := ExecuteChatWithCacheOutcome(tenantCtx(engine, "tenant-a"), req,
		func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			return chatAnswer("unexpected provider call"), nil
		})
	if err != nil || !outA2.Hit || outA2.HitType != "exact" {
		t.Fatalf("tenant A lost its own exact hit: err=%v outcome=%+v", err, outA2)
	}
}

// Reviewer scenario: tenant A's instructions carry a secret; tenant B sends the
// same user input without them and must not receive A's output.
func TestResponsesSemanticCacheDoesNotLeakAcrossTenants(t *testing.T) {
	engine := newScopedTestEngine(t)
	const input = "Repeat the supplied secret."

	_, outA, err := ExecuteResponseWithCacheOutcome(tenantCtx(engine, "tenant-a"),
		responsesReq("The secret is hunter2-TENANT-A.", input),
		func(context.Context, gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
			return responsesAnswer("hunter2-TENANT-A"), nil
		})
	if err != nil || !outA.Stored || !outA.SemanticStored {
		t.Fatalf("tenant A store failed: err=%v outcome=%+v", err, outA)
	}
	waitSemanticStore()

	var providerCalls atomic.Int32
	_, outB, err := ExecuteResponseWithCacheOutcome(tenantCtx(engine, "tenant-b"),
		responsesReq("", input),
		func(context.Context, gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
			providerCalls.Add(1)
			return responsesAnswer("no secret here"), nil
		})
	if err != nil {
		t.Fatalf("tenant B: %v", err)
	}
	if outB.Hit || providerCalls.Load() != 1 {
		t.Fatalf("tenant B received tenant A's cached Responses output (hit type %q)", outB.HitType)
	}
}

// Same tenant, different instructions: a semantic hit would return an answer
// produced under instructions the caller never sent.
func TestResponsesSemanticCacheRespectsInstructionsWithinTenant(t *testing.T) {
	engine := newScopedTestEngine(t)
	const input = "Repeat the supplied secret."

	if _, _, err := ExecuteResponseWithCacheOutcome(tenantCtx(engine, "tenant-a"),
		responsesReq("The secret is alpha.", input),
		func(context.Context, gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
			return responsesAnswer("alpha"), nil
		}); err != nil {
		t.Fatal(err)
	}
	waitSemanticStore()

	_, out, err := ExecuteResponseWithCacheOutcome(tenantCtx(engine, "tenant-a"),
		responsesReq("The secret is beta.", input),
		func(context.Context, gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
			return responsesAnswer("beta"), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hit {
		t.Fatalf("served a %s cache hit produced under different instructions", out.HitType)
	}

	// A near-duplicate phrasing under identical instructions may still hit semantically.
	_, same, err := ExecuteResponseWithCacheOutcome(tenantCtx(engine, "tenant-a"),
		responsesReq("The secret is alpha.", "Repeat the supplied secret"),
		func(context.Context, gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
			return responsesAnswer("provider"), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !same.Hit || same.HitType != "semantic" {
		t.Fatalf("expected a semantic hit within the same tenant and instructions, got %+v", same)
	}
}

func TestChatSemanticCacheRespectsSystemPromptAcrossTenants(t *testing.T) {
	engine := newScopedTestEngine(t)
	if _, _, err := ExecuteChatWithCacheOutcome(tenantCtx(engine, "tenant-a"),
		chatReq("Customer record: card ending 4242.", "What card is on file?"),
		func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			return chatAnswer("card ending 4242"), nil
		}); err != nil {
		t.Fatal(err)
	}
	waitSemanticStore()

	_, out, err := ExecuteChatWithCacheOutcome(tenantCtx(engine, "tenant-b"),
		chatReq("", "What card is on file?"),
		func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			return chatAnswer("none"), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hit {
		t.Fatalf("tenant B got tenant A's %s cache hit", out.HitType)
	}
}

// Without an authenticated tenant in context the cache must fail closed: no
// lookup can hit and nothing is stored that a later caller could read.
func TestCacheFailsClosedWithoutTenant(t *testing.T) {
	engine := newScopedTestEngine(t)
	req := chatReq("", "Anonymous question")

	for i := 0; i < 2; i++ {
		_, out, err := ExecuteChatWithCacheOutcome(tenantCtx(engine, ""), req,
			func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
				return chatAnswer("anon"), nil
			})
		if err != nil {
			t.Fatal(err)
		}
		if out.Hit {
			t.Fatalf("tenantless request %d hit the cache", i)
		}
		waitSemanticStore()
	}

	_, out, err := ExecuteChatWithCacheOutcome(tenantCtx(engine, "tenant-a"), req,
		func(context.Context, gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			return chatAnswer("tenant A"), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hit {
		t.Fatalf("tenant A read an entry written without a tenant (%s)", out.HitType)
	}
}
