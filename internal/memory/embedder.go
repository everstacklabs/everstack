package memory

import (
	"context"
	"fmt"
	"sync"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Embedder generates embeddings using gateway providers.
type Embedder struct {
	registry *gw.Registry
	router   *gw.Router
}

// NewEmbedder creates a new Embedder.
func NewEmbedder(registry *gw.Registry, router *gw.Router) *Embedder {
	return &Embedder{
		registry: registry,
		router:   router,
	}
}

// Embed generates an embedding vector for the given text.
func (e *Embedder) Embed(ctx context.Context, model, text string) ([]float32, error) {
	texts, err := e.EmbedBatch(ctx, model, []string{text})
	if err != nil {
		return nil, err
	}
	if len(texts) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return texts[0], nil
}

// EmbedBatch generates embeddings for multiple texts concurrently.
// It fans out up to 10 concurrent embedding requests to maximize throughput
// while preserving the order of results.
func (e *Embedder) EmbedBatch(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Resolve provider via router
	provider, route, err := e.router.Resolve(model)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve embedding model %q: %w", model, err)
	}

	resolvedModel := model
	if route.ModelName != "" {
		resolvedModel = route.ModelName
	}

	// Pre-allocate results slice to preserve ordering.
	embeddings := make([][]float32, len(texts))

	// Use a cancelable context so we can abort remaining goroutines on first error.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel-based semaphore limits concurrency to 10.
	sem := make(chan struct{}, 10)

	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	for i, text := range texts {
		wg.Add(1)
		go func(idx int, t string) {
			defer wg.Done()

			// Acquire semaphore slot.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errOnce.Do(func() { firstErr = ctx.Err() })
				return
			}

			req := gw.EmbeddingsRequest{
				Model: resolvedModel,
				Input: t,
			}

			resp, err := provider.Embed(ctx, req)
			if err != nil {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("embedding request failed: %w", err)
					cancel()
				})
				return
			}

			// Convert []float64 to []float32
			f32 := make([]float32, len(resp.Embedding))
			for j, v := range resp.Embedding {
				f32[j] = float32(v)
			}

			embeddings[idx] = f32
		}(i, text)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	if len(embeddings) > 0 {
		logger.WithFields("model", resolvedModel, "texts", len(texts), "dimensions", len(embeddings[0])).
			Debug("embedder: generated embeddings")
	}

	return embeddings, nil
}

// ChunkText splits text into chunks of approximately maxChunkSize characters.
// It tries to split on sentence boundaries when possible.
func ChunkText(text string, maxChunkSize int) []string {
	if maxChunkSize <= 0 {
		maxChunkSize = 512
	}
	if len(text) <= maxChunkSize {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxChunkSize {
			chunks = append(chunks, remaining)
			break
		}

		// Find a good split point (sentence boundary)
		splitAt := maxChunkSize
		for i := maxChunkSize; i > maxChunkSize/2; i-- {
			if remaining[i] == '.' || remaining[i] == '!' || remaining[i] == '?' || remaining[i] == '\n' {
				splitAt = i + 1
				break
			}
		}

		chunks = append(chunks, remaining[:splitAt])
		remaining = remaining[splitAt:]
	}

	return chunks
}
