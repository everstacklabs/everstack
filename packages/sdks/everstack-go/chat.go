package everstack

import "context"

// ChatResource provides access to chat operations.
type ChatResource struct {
	Completions *CompletionsResource
}

func newChatResource(t *Transport) *ChatResource {
	return &ChatResource{
		Completions: &CompletionsResource{t: t},
	}
}

// CompletionsResource provides chat completion operations.
type CompletionsResource struct {
	t *Transport
}

// Create creates a chat completion.
func (r *CompletionsResource) Create(ctx context.Context, params *ChatCompletionParams) (*ChatCompletionResponse, error) {
	var resp ChatCompletionResponse
	err := r.t.Request(ctx, "POST", "/v1/chat/completions", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateStream creates a streaming chat completion.
func (r *CompletionsResource) CreateStream(ctx context.Context, params *ChatCompletionParams) (*Stream[ChatCompletionChunk], error) {
	params.Stream = true
	return newStream[ChatCompletionChunk](ctx, r.t, "POST", "/v1/chat/completions", params)
}
