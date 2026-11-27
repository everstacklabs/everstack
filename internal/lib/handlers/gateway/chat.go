package gateway

import (
	"context"
	"sync"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// providerLastUsedInterval bounds how often a provider's last_used_at is
// written per tenant per process. The column says roughly when a provider was
// last called; nothing reads it to the second.
const providerLastUsedInterval = time.Minute

// providerLastUsedTouches is tenant + provider -> time of the last write.
var providerLastUsedTouches sync.Map

// touchProviderLastUsed records that a provider was just used, at most once
// per providerLastUsedInterval for a tenant and provider.
//
// It used to run on every request: one goroutine and one UPDATE of the same
// row per chat call, with no timeout. At a few hundred requests a second
// those updates queue on the row lock, each holding a pooled connection that
// requests on the same pool then wait for.
func touchProviderLastUsed(ctx context.Context, providerName string) {
	repo, ok := ctx.Value(contextkeys.ProviderRepo).(interface {
		UpdateLastUsedAt(context.Context, string) error
	})
	if !ok {
		return
	}
	key := contextkeys.ExtractTenantID(ctx) + "\x00" + providerName
	now := time.Now()
	if last, seen := providerLastUsedTouches.Load(key); seen && now.Sub(last.(time.Time)) < providerLastUsedInterval {
		return
	}
	providerLastUsedTouches.Store(key, now)
	go func() {
		// Detached from the request so a cancelled call still records its
		// use, and bounded so a stuck write cannot hold a connection.
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = repo.UpdateLastUsedAt(writeCtx, providerName)
	}()
}

// HandleChat executes a unary chat completion using the routed provider.
func HandleChat(ctx context.Context, router *Router, req ChatCompletionRequest) (ChatCompletionResponse, error) {
	provider, route, err := router.Resolve(req.Model)
	if err != nil {
		return ChatCompletionResponse{}, err
	}
	// Override model with provider-specific canonical name if provided
	if route.ModelName != "" {
		req.Model = route.ModelName
	}
	touchProviderLastUsed(ctx, route.ProviderName)

	return provider.Chat(ctx, req)
}

// HandleChatStream executes a streaming chat completion using the routed provider.
func HandleChatStream(ctx context.Context, router *Router, req ChatCompletionRequest, onChunk func(ChatResponseChunk) error) error {
	provider, route, err := router.Resolve(req.Model)
	if err != nil {
		return err
	}
	if route.ModelName != "" {
		req.Model = route.ModelName
	}
	touchProviderLastUsed(ctx, route.ProviderName)

	return provider.ChatStream(ctx, req, onChunk)
}

// ProviderAdapter is a convenience shim for building providers from separate capabilities.
type ProviderAdapter struct {
	ProviderName       string
	SupportedModelFunc func(string) bool
	ChatImpl           func(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error)
	ChatStreamImpl     func(context.Context, ChatCompletionRequest, func(ChatResponseChunk) error) error
	EmbedImpl          func(context.Context, EmbeddingsRequest) (EmbeddingsResponse, error)
}

func (p *ProviderAdapter) Name() string { return p.ProviderName }

func (p *ProviderAdapter) SupportsModel(model string) bool {
	if p.SupportedModelFunc == nil {
		return false
	}
	return p.SupportedModelFunc(model)
}

func (p *ProviderAdapter) Chat(ctx context.Context, req ChatCompletionRequest) (ChatCompletionResponse, error) {
	if p.ChatImpl == nil {
		return ChatCompletionResponse{}, ErrNotImplemented("chat")
	}
	return p.ChatImpl(ctx, req)
}

func (p *ProviderAdapter) ChatStream(ctx context.Context, req ChatCompletionRequest, onChunk func(ChatResponseChunk) error) error {
	if p.ChatStreamImpl == nil {
		return ErrNotImplemented("chat_stream")
	}
	return p.ChatStreamImpl(ctx, req, onChunk)
}

func (p *ProviderAdapter) Embed(ctx context.Context, req EmbeddingsRequest) (EmbeddingsResponse, error) {
	if p.EmbedImpl == nil {
		return EmbeddingsResponse{}, ErrNotImplemented("embed")
	}
	return p.EmbedImpl(ctx, req)
}
