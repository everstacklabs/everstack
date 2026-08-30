package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/fastpath"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/streaming"
)

// StreamSender wraps Connect's stream to send ChatResponseChunk items.
type StreamSender interface {
	Send(*ChatResponseChunk) error
}

// StreamChat handles streaming chat chunks via Connect streaming response.
func StreamChat(ctx context.Context, sender StreamSender, providerChat func(ctx context.Context, onChunk func(ChatResponseChunk) error) error) error {
	return providerChat(ctx, func(chunk ChatResponseChunk) error {
		return sender.Send(&chunk)
	})
}

// WithStreamHeaders sets standard streaming headers for SSE-like responses.
func WithStreamHeaders(header http.Header) {
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("Content-Type", "application/json; charset=utf-8")
}

// SSEStreamSender implements StreamSender by emitting JSON-encoded SSE events.
type SSEStreamSender struct {
	w   http.ResponseWriter
	enc *json.Encoder
}

// NewSSEStreamSender configures headers and returns an SSE sender.
func NewSSEStreamSender(w http.ResponseWriter) *SSEStreamSender {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	return &SSEStreamSender{w: w, enc: json.NewEncoder(w)}
}

func (s *SSEStreamSender) Send(chunk *ChatResponseChunk) error {
	// SSE format: data: <json>\n\n
	if _, err := s.w.Write([]byte("data: ")); err != nil {
		return err
	}
	if err := s.enc.Encode(chunk); err != nil {
		return err
	}
	if _, err := s.w.Write([]byte("\n")); err != nil {
		return err
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// PooledSSEStreamSender implements StreamSender using pooled buffers for zero-allocation streaming.
// This is the fast-path version of SSEStreamSender.
type PooledSSEStreamSender struct {
	writer *streaming.PooledSSEWriter
	closed atomic.Bool
}

// NewPooledSSEStreamSender creates a new pooled SSE sender.
// The caller must call Close() when done to return buffers to the pool.
func NewPooledSSEStreamSender(w http.ResponseWriter) *PooledSSEStreamSender {
	return &PooledSSEStreamSender{
		writer: streaming.NewPooledSSEWriter(w),
	}
}

// Send sends a chunk as an SSE event using pooled buffers.
func (s *PooledSSEStreamSender) Send(chunk *ChatResponseChunk) error {
	if s.closed.Load() {
		return nil
	}

	// Use fast-path JSON marshaling if available
	data, err := fastpath.Marshal(chunk)
	if err != nil {
		return err
	}

	return s.writer.WriteEvent(data)
}

// Close returns the pooled buffers. Always call this when done.
func (s *PooledSSEStreamSender) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	return s.writer.Close()
}

// Stats returns the sender's statistics.
func (s *PooledSSEStreamSender) Stats() (events, bytes, flushes uint64) {
	return s.writer.Stats()
}

// ChatStreamGenerator exposes provider streaming as a channel-based generator.
// The returned channel is closed when the stream ends. Any terminal error is
// sent on errCh after the data channel closes. Call cancel() to stop early.
func ChatStreamGenerator(ctx context.Context, router *Router, req ChatCompletionRequest) (<-chan ChatResponseChunk, <-chan error, context.CancelFunc) {
	out := make(chan ChatResponseChunk, 8)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		defer close(out)
		defer close(errCh)
		provider, _, err := router.Resolve(req.Model)
		if err != nil {
			errCh <- err
			return
		}
		err = provider.ChatStream(ctx, req, func(chunk ChatResponseChunk) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- chunk:
				return nil
			}
		})
		// Avoid blocking if nobody is listening; drop after timeout
		select {
		case errCh <- err:
		case <-time.After(100 * time.Millisecond):
		}
	}()

	return out, errCh, cancel
}
