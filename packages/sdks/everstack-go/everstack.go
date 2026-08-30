// Package everstack provides the official Go SDK for the Everstack AI platform.
//
// # Quick Start
//
//	client := everstack.NewClient("pk_...")
//	resp, err := client.Chat.Completions.Create(ctx, &everstack.ChatCompletionParams{
//	    Model:    "@openai/gpt-4o",
//	    Messages: []everstack.Message{{Role: "user", Content: "Hello!"}},
//	})
//	fmt.Println(resp.Choices[0].Message.Content)
package everstack

import "net/http"

const (
	DefaultBaseURL = "https://gateway.everstack.ai"
	defaultTimeout = 60 // seconds
)

// Client is the Everstack API client.
type Client struct {
	Chat          *ChatResource
	Embeddings    *EmbeddingsResource
	Models        *ModelsResource
	Audio         *AudioResource
	Images        *ImagesResource
	Moderations   *ModerationsResource
	Rerank        *RerankResource
	Responses     *ResponsesResource
	Agents        *AgentsResource
	Datasets      *DatasetsResource
	Evaluations   *EvaluationsResource
	Observability *ObservabilityResource
	Traces        *TracesResource

	transport *Transport
}

// NewClient creates a new Everstack client with the given API key and options.
func NewClient(apiKey string, opts ...Option) *Client {
	cfg := &config{
		baseURL:    DefaultBaseURL,
		apiKey:     apiKey,
		httpClient: http.DefaultClient,
		timeout:    defaultTimeout,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	t := newTransport(cfg)
	c := &Client{transport: t}

	c.Chat = newChatResource(t)
	c.Embeddings = &EmbeddingsResource{t: t}
	c.Models = &ModelsResource{t: t}
	c.Audio = newAudioResource(t)
	c.Images = &ImagesResource{t: t}
	c.Moderations = &ModerationsResource{t: t}
	c.Rerank = &RerankResource{t: t}
	c.Responses = &ResponsesResource{t: t}
	c.Agents = newAgentsResource(t)
	c.Datasets = newDatasetsResource(t)
	c.Evaluations = newEvaluationsResource(t)
	c.Observability = newObservabilityResource(t)
	c.Traces = newTracesResource(t)

	return c
}
