package v1

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/compact"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/providers/logging"
	"github.com/everstacklabs/everstack/internal/providers/tracing"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/sirupsen/logrus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// instantModel is an upstream that answers at once with a 64-token
// completion, so the benchmark measures the gateway's own work per request.
type instantModel struct {
	reply string
}

func newInstantModel() *instantModel {
	var text strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&text, "tok%d ", i)
	}
	return &instantModel{reply: text.String()}
}

func (p *instantModel) Name() string                { return "pp" }
func (p *instantModel) SupportsModel(m string) bool { return m == "m-primary" }
func (p *instantModel) Chat(_ context.Context, r gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	return gw.ChatCompletionResponse{
		ID:      "chatcmpl-bench",
		Model:   r.Model,
		Choices: []gw.Choice{{Message: gw.NewMessage(gw.RoleAssistant, gw.Text(p.reply)), FinishReason: "stop"}},
		Usage:   gw.Usage{PromptTokens: 100, CompletionTokens: 64, TotalTokens: 164},
	}, nil
}
func (p *instantModel) ChatStream(context.Context, gw.ChatCompletionRequest, func(gw.ChatResponseChunk) error) error {
	return fmt.Errorf("instantModel does not stream")
}
func (p *instantModel) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, fmt.Errorf("instantModel does not embed")
}

// discardSpans receives finished spans and drops them, standing in for the
// OTLP exporter so span creation and attribute work are still paid for.
type discardSpans struct{}

func (discardSpans) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (discardSpans) Shutdown(context.Context) error                             { return nil }

// BenchmarkChatHandlerUnary measures the chat handler's own cost for one
// non-streaming completion: request normalisation, routing, the provider
// decorators (logging, compaction, tracing) and every span it records, with
// the upstream answering instantly and tracing on through a batch processor.
//
// It runs at two log levels because they cost very differently. info is the
// shipped default (server.log.level) and logs about a dozen lines a request;
// warn logs none. Log output is discarded, so the info figure still leaves
// out writing the lines and exporting them over OTLP.
//
// It also leaves out what needs a running deployment: authentication, the
// REST hop (see BenchmarkRESTChatHop), the response cache and event storage.
func BenchmarkChatHandlerUnary(b *testing.B) {
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(discardSpans{})),
	)
	previous := telemetry.GetGlobalTracerProvider()
	telemetry.SetGlobalTracerProvider(tracerProvider)
	b.Cleanup(func() {
		telemetry.SetGlobalTracerProvider(previous)
		_ = tracerProvider.Shutdown(context.Background())
	})
	logger.SetOutput(io.Discard)

	cat := catalog.NewCache()
	if err := cat.Load([]byte(attemptCatalog), []byte("providers: {}\n")); err != nil {
		b.Fatal(err)
	}
	cfg := &validator.GatewayConfig{LoadBalancer: validator.LoadBalancerConfig{Fallback: validator.FallbackConfig{Enabled: true}}}
	s := CreateServerWithContext(context.Background(), cfg, &validator.FeaturesConfig{})
	s.feat.Gateway.EnableStreaming = true

	// The same decorator order the provider factories build.
	base := newInstantModel()
	traced := tracing.NewMiddlewareWithKey(base, cat, "k", "bench", "yaml")
	compacted := compact.NewMiddleware(traced, base.Name(), compact.Config{}, nil)
	s.defaultBundle.Load().reg.Register(logging.NewMiddleware(compacted, logging.SelectExtractor(base.Name())))

	// 400 character prompts, each different, as the gateway benchmark sends.
	const filler = "benchmark payload token stream measurement control plane routing "
	prompts := make([]string, 1024)
	for i := range prompts {
		prompts[i] = (fmt.Sprintf("[req-%d] ", i) + strings.Repeat(filler, 7))[:400]
	}
	ctx := contextkeys.WithTenantID(context.Background(), "tenant-1")
	stream := false
	send := func(*pb.ChatCompletionResponse) error { return nil }
	request := func(i int) *pb.ChatCompletionRequest {
		return &pb.ChatCompletionRequest{
			Model:  "m-primary",
			Stream: &stream,
			Messages: []*pb.Message{{
				Role:    pb.Role_ROLE_USER,
				Content: []*pb.ContentPart{{Type: "text", Data: &pb.ContentPart_Text{Text: prompts[i%len(prompts)]}}},
			}},
		}
	}

	if err := processChatCompletion(ctx, s, request(0), send); err != nil {
		b.Fatalf("setup request failed: %v", err)
	}

	for _, level := range []logrus.Level{logrus.InfoLevel, logrus.WarnLevel} {
		b.Run("log="+level.String(), func(b *testing.B) {
			logger.SetLevel(level)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := processChatCompletion(ctx, s, request(i), send); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
