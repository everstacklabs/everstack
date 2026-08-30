package gateway

import (
	"context"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

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
	// Update last used at timestamp (fire and forget)
	if repo, ok := ctx.Value(contextkeys.ProviderRepo).(interface {
		UpdateLastUsedAt(context.Context, string) error
	}); ok {
		go func() {
			// Use a detached context to ensure it completes even if request is canceled
			_ = repo.UpdateLastUsedAt(context.Background(), route.ProviderName)
		}()
	}

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
	// Update last used at timestamp (fire and forget)
	if repo, ok := ctx.Value(contextkeys.ProviderRepo).(interface {
		UpdateLastUsedAt(context.Context, string) error
	}); ok {
		go func() {
			// Use a detached context to ensure it completes even if request is canceled
			_ = repo.UpdateLastUsedAt(context.Background(), route.ProviderName)
		}()
	}

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
