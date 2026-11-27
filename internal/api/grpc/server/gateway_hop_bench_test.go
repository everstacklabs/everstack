package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"
	gatewayv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// chatStub answers ChatCompletion with one frame, the shape of a unary
// completion: a 64-token message and its usage.
type chatStub struct {
	gatewayconnect.UnimplementedGatewayServiceHandler
	reply *gatewayv1.ChatCompletionResponse
}

func newChatStub() *chatStub {
	var text strings.Builder
	for i := 0; i < 64; i++ {
		text.WriteString("tok")
		text.WriteString(strconv.Itoa(i))
		text.WriteByte(' ')
	}
	return &chatStub{reply: &gatewayv1.ChatCompletionResponse{
		Id:      "chatcmpl-bench",
		Created: 1700000000,
		Model:   "gpt-4o-mini",
		Choices: []*gatewayv1.Choice{{
			Message: &gatewayv1.Message{
				Role:    gatewayv1.Role_ROLE_ASSISTANT,
				Content: []*gatewayv1.ContentPart{{Type: "text", Data: &gatewayv1.ContentPart_Text{Text: text.String()}}},
			},
			FinishReason: "stop",
		}},
		Usage: &gatewayv1.Usage{PromptTokens: 100, CompletionTokens: 64, TotalTokens: 164},
	}}
}

func (s *chatStub) ChatCompletion(_ context.Context, _ *connect.Request[gatewayv1.ChatCompletionRequest], stream *connect.ServerStream[gatewayv1.ChatCompletionResponse]) error {
	return stream.Send(s.reply)
}

// chatRequestBody is a chat request as the REST gateway receives it after the
// OpenAI transform: a 400 character prompt, the size the benchmark uses.
func chatRequestBody() string {
	prompt := strings.Repeat("benchmark payload token stream measurement control plane routing ", 7)[:400]
	return `{"model":"gpt-4o-mini","messages":[{"role":"ROLE_USER","content":[{"type":"text","text":"` + prompt +
		`"}]}],"sampling":{"temperature":0,"max_tokens":64},"stream":false}`
}

func connectChatTree() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(gatewayconnect.NewGatewayServiceHandler(newChatStub()))
	return mux
}

// BenchmarkRESTChatHop measures what one unary chat completion costs between
// the REST handler and the Connect handler and back, with nothing else in the
// way: JSON to proto, the gRPC call into this process, and proto back to JSON.
// The two cases differ only in the transport under that call.
func BenchmarkRESTChatHop(b *testing.B) {
	b.Run("direct", func(b *testing.B) {
		ctx, cancel := context.WithCancel(WithLoopback(context.Background()))
		defer cancel()
		rest, err := NewGRPCJSONGateway(ctx, "gateway.invalid", 1, nil)
		if err != nil {
			b.Fatalf("NewGRPCJSONGateway: %v", err)
		}
		stop := LoopbackFromContext(ctx).Serve(connectChatTree())
		defer stop()
		benchChatHop(b, rest)
	})

	b.Run("loopback", func(b *testing.B) {
		b.Setenv(loopbackTransportEnv, "memory")
		ctx, cancel := context.WithCancel(WithLoopback(context.Background()))
		defer cancel()
		rest, err := NewGRPCJSONGateway(ctx, "gateway.invalid", 1, nil)
		if err != nil {
			b.Fatalf("NewGRPCJSONGateway: %v", err)
		}
		stop := LoopbackFromContext(ctx).Serve(connectChatTree())
		defer stop()
		benchChatHop(b, rest)
	})

	b.Run("tcp", func(b *testing.B) {
		// The previous arrangement: an h2c listener on a real socket.
		srv := httptest.NewServer(h2c.NewHandler(connectChatTree(), &http2.Server{}))
		defer srv.Close()
		_, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
		if err != nil {
			b.Fatalf("listener address: %v", err)
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			b.Fatalf("listener port: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rest, err := NewGRPCJSONGateway(ctx, "127.0.0.1", uint16(port), nil)
		if err != nil {
			b.Fatalf("NewGRPCJSONGateway: %v", err)
		}
		benchChatHop(b, rest)
	})
}

func benchChatHop(b *testing.B, rest http.Handler) {
	body := chatRequestBody()

	// One request up front, so a broken setup fails loudly instead of being
	// measured as a very fast error.
	rec := httptest.NewRecorder()
	rest.ServeHTTP(rec, newChatRequest(body))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "tok63") {
		b.Fatalf("setup request failed: %d %s", rec.Code, rec.Body.String())
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w := &discardResponseWriter{header: http.Header{}}
			rest.ServeHTTP(w, newChatRequest(body))
			if w.status != 0 && w.status != http.StatusOK {
				b.Errorf("status %d", w.status)
				return
			}
		}
	})
}

func newChatRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// discardResponseWriter drops the body so the benchmark measures the hop, not
// a recorder growing a buffer.
type discardResponseWriter struct {
	header http.Header
	status int
}

func (w *discardResponseWriter) Header() http.Header         { return w.header }
func (w *discardResponseWriter) WriteHeader(status int)      { w.status = status }
func (w *discardResponseWriter) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (w *discardResponseWriter) Flush()                      {}
