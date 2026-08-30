# Everstack Go SDK — Implementation Plan

## Overview

`everstack-go` — An idiomatic Go SDK using Connect-RPC (same protocol as the Node SDK) with full type safety from generated proto stubs.

**Module:** `github.com/everstacklabs/everstack-go`
**Min Go:** 1.22+

---

## Project Structure

```
everstack-go/
├── everstack.go              # Client struct, options, constructor
├── errors.go                 # Error types and Connect error mapping
├── transport.go              # Connect HTTP client, interceptors
├── compat.go                 # OpenAI SDK compatibility helpers
├── chat.go                   # Chat completions resource
├── embeddings.go             # Embeddings resource
├── models.go                 # Model listing resource
├── audio.go                  # Audio (speech, transcription, translation)
├── images.go                 # Images (generate, edit, variation)
├── moderations.go            # Content moderation
├── rerank.go                 # Document reranking
├── responses.go              # Responses API (agentic orchestration)
├── agents.go                 # Agent lifecycle, sessions, sandboxes, etc.
├── datasets.go               # Datasets CRUD + items + score configs
├── evaluations.go            # Eval runs + schedules
├── observability.go          # Metrics, sessions, users, outcomes
├── memory.go                 # Vector memory (REST-based)
├── catalog/
│   └── models.go             # Generated model constants + metadata
├── option/
│   └── option.go             # Functional options pattern
├── internal/
│   └── gen/                  # Generated Connect-RPC stubs (from buf)
│       ├── gateway/v1/
│       ├── agents/v1/
│       ├── datasets/v1/
│       └── traces/v1/
├── examples/
│   ├── chat/main.go
│   ├── streaming/main.go
│   ├── agents/main.go
│   ├── datasets/main.go
│   ├── audio/main.go
│   ├── images/main.go
│   └── responses/main.go
├── go.mod
├── go.sum
└── README.md
```

---

## Dependencies

| Module | Purpose |
|--------|---------|
| `connectrpc.com/connect` | Connect-RPC client (same protocol as Node SDK) |
| `google.golang.org/protobuf` | Proto message runtime |
| `net/http` (stdlib) | HTTP/2 transport |

**No other external deps required.** Go's stdlib + Connect-RPC covers everything.

**Dev/codegen:**
- `buf` CLI for proto codegen
- `connectrpc.com/connect` protoc plugin

---

## Proto Codegen Strategy

Reuse the monorepo's `proto/` definitions with `buf generate`:

```yaml
# buf.gen.go.yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: internal/gen
    opt: paths=source_relative
  - remote: buf.build/connectrpc/go
    out: internal/gen
    opt: paths=source_relative
```

Generated stubs go into `internal/gen/` (unexported). The public SDK types are hand-written Go structs with JSON tags for ergonomics.

---

## API Design

### Client Construction (Functional Options)

```go
import "github.com/everstacklabs/everstack-go"

// Simple
client := everstack.NewClient("pk_...")

// With options
client := everstack.NewClient("pk_...",
    everstack.WithBaseURL("http://localhost:8080"),
    everstack.WithProvider("@openai"),
    everstack.WithOrgID("org_123"),
    everstack.WithHTTPClient(customClient),
    everstack.WithTimeout(30 * time.Second),
)
```

### Resource Pattern

```go
// Chat completions
resp, err := client.Chat.Completions.Create(ctx, &everstack.ChatCompletionParams{
    Model:    "@openai/gpt-4o",
    Messages: []everstack.Message{
        {Role: "user", Content: "Hello!"},
    },
})
fmt.Println(resp.Choices[0].Message.Content)

// Streaming (iter pattern)
stream, err := client.Chat.Completions.CreateStream(ctx, &everstack.ChatCompletionParams{
    Model:    "@openai/gpt-4o",
    Messages: []everstack.Message{
        {Role: "user", Content: "Hello!"},
    },
})
defer stream.Close()

for stream.Next() {
    chunk := stream.Current()
    fmt.Print(chunk.Choices[0].Delta.Content)
}
if err := stream.Err(); err != nil {
    log.Fatal(err)
}
```

### Full Resource Surface

```go
client.Chat.Completions.Create(ctx, params)
client.Chat.Completions.CreateStream(ctx, params)

client.Embeddings.Create(ctx, params)

client.Models.List(ctx)

client.Audio.Speech.Create(ctx, params)
client.Audio.Transcriptions.Create(ctx, params)
client.Audio.Translations.Create(ctx, params)

client.Images.Generate(ctx, params)
client.Images.Edit(ctx, params)
client.Images.CreateVariation(ctx, params)

client.Moderations.Create(ctx, params)

client.Rerank.Create(ctx, params)

client.Responses.Create(ctx, params)
client.Responses.CreateStream(ctx, params)
client.Responses.Get(ctx, responseID)
client.Responses.Cancel(ctx, responseID)
client.Responses.Delete(ctx, responseID)
client.Responses.List(ctx, params)

client.Agents.Create(ctx, params)
client.Agents.Get(ctx, agentID)
client.Agents.List(ctx, params)
client.Agents.Update(ctx, params)
client.Agents.Delete(ctx, agentID)
client.Agents.Sessions.Create(ctx, params)
client.Agents.Sessions.RunTurn(ctx, params)
client.Agents.Sessions.RunTurnStream(ctx, params)
client.Agents.Lifecycle.Provision(ctx, params)
client.Agents.Lifecycle.Sleep(ctx, params)
client.Agents.Lifecycle.Wake(ctx, params)
// ... full agent surface

client.Datasets.Create(ctx, params)
client.Datasets.Items.CreateBatch(ctx, params)
client.Evaluations.Runs.Create(ctx, params)
client.Evaluations.Schedules.Create(ctx, params)

client.Observability.Metrics.GetDashboard(ctx, params)
client.Observability.Sessions.List(ctx, params)
client.Observability.Outcomes.GetDashboard(ctx, params)

client.Memory.Collections.Create(ctx, params)
client.Memory.Collections.Query(ctx, name, params)
```

---

## Type Design

```go
// Request types — exported structs with JSON tags
type ChatCompletionParams struct {
    Model       string    `json:"model"`
    Messages    []Message `json:"messages"`
    Temperature *float64  `json:"temperature,omitempty"`
    TopP        *float64  `json:"top_p,omitempty"`
    MaxTokens   *int      `json:"max_tokens,omitempty"`
    Stop        []string  `json:"stop,omitempty"`
    Stream      bool      `json:"stream,omitempty"`
}

// Response types — exported structs
type ChatCompletionResponse struct {
    ID      string                  `json:"id"`
    Object  string                  `json:"object"`
    Created int64                   `json:"created"`
    Model   string                  `json:"model"`
    Choices []ChatCompletionChoice  `json:"choices"`
    Usage   Usage                   `json:"usage"`
}

// Streaming — generic Stream[T] type
type Stream[T any] struct { ... }
func (s *Stream[T]) Next() bool
func (s *Stream[T]) Current() T
func (s *Stream[T]) Err() error
func (s *Stream[T]) Close() error
```

---

## Error Handling

```go
// Error types
type Error struct {
    StatusCode int
    Code       string
    Message    string
    Param      string
}

func (e *Error) Error() string

// Sentinel errors for type switching
var (
    ErrAuthentication     = ...
    ErrPermissionDenied   = ...
    ErrNotFound           = ...
    ErrRateLimited        = ...
    ErrInternalServer     = ...
    ErrServiceUnavailable = ...
    ErrTimeout            = ...
    ErrConnection         = ...
)

// Usage
resp, err := client.Chat.Completions.Create(ctx, params)
if err != nil {
    var apiErr *everstack.Error
    if errors.As(err, &apiErr) {
        fmt.Printf("API error %d: %s\n", apiErr.StatusCode, apiErr.Message)
    }
}
```

---

## Streaming Design

Use a generic `Stream[T]` that wraps Connect-RPC server streams:

```go
type Stream[T any] struct {
    stream *connect.ServerStreamForClient[proto.Message]
    decode func(proto.Message) T
    curr   T
    err    error
}

// Iterator pattern (idiomatic Go)
for stream.Next() {
    chunk := stream.Current()
    // process chunk
}
if err := stream.Err(); err != nil {
    // handle error
}
defer stream.Close()
```

---

## Connect-RPC Transport

```go
// Uses same Connect protocol as Node SDK
// HTTP/2 by default, falls back to HTTP/1.1

func newTransport(opts *Options) connect.HTTPClient {
    return &http.Client{
        Transport: &headerTransport{
            base:     http.DefaultTransport,
            apiKey:   opts.APIKey,
            provider: opts.Provider,
            orgID:    opts.OrgID,
            userID:   opts.UserID,
            headers:  opts.Headers,
        },
    }
}
```

---

## Model Catalog

```go
package catalog

// Generated constants
const (
    OpenAI_GPT4o         = "@openai/gpt-4o"
    OpenAI_GPT4oMini     = "@openai/gpt-4o-mini"
    Anthropic_Claude4     = "@anthropic/claude-opus-4-latest"
    Google_Gemini15Pro    = "@google/gemini-1.5-pro"
    Qwen_Qwen3Max        = "@qwen/qwen3-max"
    DeepSeek_Chat        = "@deepseek/deepseek-chat"
    // ... all models
)

type ModelMetadata struct {
    ID           string
    Provider     string
    Model        string
    DisplayName  string
    Capabilities []string
    MaxTokens    int
    Status       string
}

func GetMetadata(modelID string) (ModelMetadata, bool)
func ListByProvider(provider string) []ModelMetadata
func IsValid(modelID string) bool
```

---

## OpenAI Compatibility

```go
import "github.com/everstacklabs/everstack-go/compat"

// Returns config for use with sashabaranov/go-openai
config := compat.OpenAIConfig("pk_...", "@openai")
openaiClient := openai.NewClientWithConfig(config)
```

---

## Build & CI

- `go test ./...` — unit + integration tests
- `go vet ./...` — static analysis
- `golangci-lint run` — comprehensive linting
- `buf generate` — proto codegen
- Tag-based releases: `v0.1.0`, `v0.2.0`, etc.

---

## Implementation Order

1. Module setup, transport, errors
2. Chat completions (non-streaming + streaming with `Stream[T]`)
3. Embeddings + Models
4. Audio, Images, Moderations, Rerank
5. Responses API
6. Agents (largest resource)
7. Datasets + Evaluations
8. Observability
9. Memory (REST-based, `net/http`)
10. Model catalog codegen script
11. OpenAI compat layer
12. Examples
