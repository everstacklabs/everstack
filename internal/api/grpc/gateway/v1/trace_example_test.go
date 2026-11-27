package v1

// TestGatewayTraceExample replays the exact attribute sequence
// processChatCompletion emits on the unary success path — same helpers, same
// order, same values — and dumps the resulting spans as they would land in
// ClickHouse. This is the review artifact for the EVS-83 perf work: it proves
// the stored trace keeps full-fidelity payloads (no truncation markers,
// complete text) after the dedup/marshal/reorder changes.
//
// Run: go test ./internal/api/grpc/gateway/v1/ -run TestGatewayTraceExample -v
// The dumped span JSON is the "example of the final trace" for review.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
)

const exampleUserCode = `package main

import "fmt"

func main() {
	//ZGHTEST-MARKER-7f3a9c: the tail of this prompt must appear verbatim
	// in trace.input or the trace lost data.
	fmt.Println("hello <world> & \"everyone\" — café 🎉")
}`

const exampleAnswerTail = `Done. The fix is deployed — verified with café ordering 🎉. ZGHTEST-ANSWER-MARKER-4b1e22.`

func exampleMessages() []gw.Message {
	return []gw.Message{
		gw.NewMessage(gw.RoleSystem, gw.Text("You are a senior Go engineer. Be precise. Follow the repo's error-handling conventions.")),

		gw.NewMessage(gw.RoleUser, gw.Text("Review this program for data races:\n\n```go\n"+exampleUserCode+"\n```")),
		{
			Role:    gw.RoleAssistant,
			Content: []gw.ContentPart{gw.Text("I'll run the race detector on it first.")},
			ToolCalls: []gw.ToolCall{
				{ID: "call_exec_1", Type: "function", Function: gw.ToolCallFunction{
					Name:      "run_tests",
					Arguments: `{"package": "./...", "race": true, "timeout": "120s"}`,
				}},
			},
		},
		{
			Role: gw.RoleTool,
			Content: []gw.ContentPart{
				{Type: "text", Text: exampleStrPtr("ok 1.2s — no races detected"), ToolCallID: exampleStrPtr("call_exec_1")},
			},
		},
	}
}

func exampleResponse() gw.ChatCompletionResponse {
	return gw.ChatCompletionResponse{
		ID:      "chatcmpl-example123",
		Created: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
		Model:   "gpt-4o",
		Choices: []gw.Choice{
			{
				Index:        0,
				Message:      gw.Message{Role: gw.RoleAssistant, Content: []gw.ContentPart{gw.Text("No races. `fmt.Println` is safe here. " + exampleAnswerTail)}},
				FinishReason: "stop",
			},
		},
		Usage: gw.Usage{
			PromptTokens:     1843,
			CompletionTokens: 96,
			TotalTokens:      1939,
			PromptDetails:    &gw.TokenDetails{CachedTokens: 1500, CacheReadTokens: 1500, TextTokens: 343},
		},
	}
}

func dumpSpans(t *testing.T, spans []tracetest.SpanStub) string {
	t.Helper()
	out := make(map[string]map[string]interface{}, len(spans))
	for _, s := range spans {
		m := make(map[string]interface{}, len(s.Attributes)+2)
		m["_span_name"] = s.Name
		for _, a := range s.Attributes {
			m[string(a.Key)] = attrValue(a.Value)
		}
		out[s.SpanContext.SpanID().String()] = m
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("dump spans: %v", err)
	}
	return string(b)
}

func attrValue(v attribute.Value) interface{} {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.STRING:
		return v.AsString()
	case attribute.BOOLSLICE:
		return v.AsBoolSlice()
	case attribute.INT64SLICE:
		return v.AsInt64Slice()
	case attribute.FLOAT64SLICE:
		return v.AsFloat64Slice()
	case attribute.STRINGSLICE:
		return v.AsStringSlice()
	default:
		return v.Emit()
	}
}

func TestGatewayTraceExample(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	tracer := tp.Tracer("gateway-example")

	norm := gw.ChatCompletionRequest{
		Model:    "gpt-4o",
		Messages: exampleMessages(),
		Sampling: gw.SamplingParams{Temperature: 0.7, TopP: 0.9, MaxTokens: 4096},
		Stream:   false,
	}
	resp := exampleResponse()
	correlationID := "chat_example01"

	// ---- root span + normalization span, gateway order ----
	ctx, span := tracer.Start(t.Context(), "gateway.chat.completion")
	_, normSpan := tracer.Start(ctx, "gateway.request.normalization")

	msgMetadata := attrs.AnalyzeMessages(norm.Messages)
	attrs.SetRequestMetadata(normSpan, len(norm.Messages), norm.Stream, norm.Sampling)
	attrs.SetMessageStructure(normSpan, msgMetadata)
	attrs.SetBusinessContext(normSpan, "user_123", "apikeyhash_abc", "")

	// One shared marshal, exactly like the gateway hot path now does.
	normMessagesJSON := perfSerializeToJSON(norm.Messages)
	attrs.SetNormalizationMetadata(normSpan, 3, true)
	attrs.SetNormalizationDetails(normSpan,
		perfSerializeToJSON(norm.Messages),
		normMessagesJSON,
		"1.2", "strip_whitespace,validate_schema,add_defaults", 3, true, "[]", "[]",
		"openai-compatible-v1")
	telemetry.AddSpanEvent(normSpan, attrs.EventNormalizationComplete, attribute.Bool("output_valid", true))
	attrs.SetTraceInput(span, norm.Messages)
	attrs.SetRequestDetails(span, correlationID, normMessagesJSON, "", "", msgMetadata.TotalChars, len(normMessagesJSON))
	normSpan.End()

	// ---- model resolution + provider (values only; no upstream call) ----
	span.SetAttributes(
		attribute.String("provider", "openai"),
		attribute.String("model.served", "gpt-4o"),
		attribute.Int64("latency_ms", 912),
		attribute.Bool("fallback.triggered", false),
	)
	attrs.SetLLMTokens(span, int64(resp.Usage.PromptTokens), int64(resp.Usage.CompletionTokens), int64(resp.Usage.TotalTokens))

	// ---- response processing, gateway order ----
	_, respSpan := tracer.Start(ctx, "gateway.response.processing")
	respMetadata := attrs.AnalyzeResponse(resp)
	attrs.SetResponseProcessing(respSpan, resp.Model, resp.ID, respMetadata, 2)
	outputJSON := perfSerializeToJSON(resp.Choices)
	attrs.SetResponseDetails(respSpan, outputJSON, "stop", resp.Model, resp.Usage.CompletionTokens, len(outputJSON))
	telemetry.AddSpanEvent(respSpan, attrs.EventResponseComplete,
		attribute.Int("response.tokens", resp.Usage.CompletionTokens),
		attribute.Bool("response.cached", false))
	respSpan.SetStatus(codes.Ok, "response processed")
	respSpan.End()
	attrs.SetTraceOutput(span, resp.Choices)

	costDetails := metrics.CalculateCostWithCacheTokens("openai", resp.Model,
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens,
		resp.Usage.CacheReadCount(), resp.Usage.CacheWriteCount())
	attrs.SetCostDetails(span, costDetails.EstimatedUSD, costDetails.ActualUSD, costDetails.SavingsUSD, costDetails.CarbonSavedGrams, costDetails.PricingModel)
	attrs.SetRootSpanBusinessMetrics(span,
		costDetails.EstimatedUSD*0.3, costDetails.EstimatedUSD*0.7, costDetails.EstimatedUSD,
		costDetails.SavingsUSD, 0, costDetails.CarbonSavedGrams,
		int64(resp.Usage.PromptTokens), int64(resp.Usage.CompletionTokens), int64(resp.Usage.TotalTokens),
		912, 0, 912)

	// ---- post-send classification (runs after send on the hot path) ----
	attrs.SetPerformanceMetrics(span, 912, 0, 0, 0, 2.1, "acceptable")
	businessMetrics := classifyBusinessMetrics(norm.Messages, resp)
	attrs.SetBusinessMetrics(span, businessMetrics.UseCase, businessMetrics.Domain, businessMetrics.QueryType, businessMetrics.QualityScore)
	span.End()

	stubs := exporter.GetSpans()
	if len(stubs) != 3 {
		t.Fatalf("expected 3 spans (root+normalize+response), got %d", len(stubs))
	}
	t.Logf("FINAL TRACE AS STORED (spanID -> attributes):\n%s", dumpSpans(t, stubs))

	// ---- fidelity assertions: the whole point of this artifact ----
	root := findSpan(t, stubs, "gateway.chat.completion")
	strAttrs := map[string]string{}
	for _, a := range root.Attributes {
		if a.Value.Type() == attribute.STRING {
			strAttrs[string(a.Key)] = a.Value.AsString()
		}
	}
	for key, val := range strAttrs {
		if strings.Contains(val, "...[truncated]") {
			t.Errorf("attribute %s carries a truncation marker — trace data was cut", key)
		}
	}
	if got := strAttrs["trace.input"]; !strings.Contains(got, "ZGHTEST-MARKER-7f3a9c") {
		t.Errorf("trace.input lost the tail of the prompt; got %d chars", len(got))
	}
	if got := strAttrs["trace.input"]; !strings.Contains(got, "café 🎉") {
		t.Errorf("trace.input mangled unicode/HTML chars")
	}
	if got := strAttrs["request.input"]; got != strAttrs["trace.input"] {
		// Both derive from the same shared marshal: dedup must not diverge them.
		// (trace.input passes through maybeRedact; RedactPII defaults off.)
		t.Errorf("request.input diverged from trace.input — shared marshal broken")
	}
	if got := strAttrs["trace.output"]; !strings.Contains(got, "ZGHTEST-ANSWER-MARKER-4b1e22") {
		t.Errorf("trace.output lost the tail of the answer; got %d chars", len(got))
	}
	// response.output lives on the response-processing span, trace.output on
	// the root span; both derive from the same choices payload and must agree.
	respStub := findSpan(t, stubs, "gateway.response.processing")
	var respOutput string
	for _, a := range respStub.Attributes {
		if string(a.Key) == "response.output" && a.Value.Type() == attribute.STRING {
			respOutput = a.Value.AsString()
		}
	}
	if respOutput != strAttrs["trace.output"] {
		t.Errorf("response.output diverged from trace.output")
	}
	for _, k := range []string{"business.use_case", "business.domain", "business.query_type", "performance.latency_category"} {
		found := false
		for _, a := range root.Attributes {
			if string(a.Key) == k {
				found = true
			}
		}
		if !found {
			t.Errorf("post-send attribute %s missing from stored span", k)
		}
	}
}

// TestGatewayTraceLargePayload proves the no-truncation guarantee at a size
// where the old unbounded code and any capped code would visibly differ: a
// ~100KB prompt must round-trip into trace.input completely.
func TestGatewayTraceLargePayload(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	tracer := tp.Tracer("gateway-example-large")

	bigText := "PROMPT-BODY " + strings.Repeat("lorem ipsum dolor sit amet <>&\" — ", 2800) + " PROMPT-TAIL-ZGHTEST-9d2c11"
	msgs := []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text(bigText))}

	_, span := tracer.Start(t.Context(), "gateway.chat.completion")
	attrs.SetTraceInput(span, msgs)
	attrs.SetRequestDetails(span, "chat_big01", perfSerializeToJSON(msgs), "", "", len(bigText), len(bigText))
	span.End()

	stubs := exporter.GetSpans()
	if len(stubs) != 1 {
		t.Fatalf("expected 1 span, got %d", len(stubs))
	}
	for _, a := range stubs[0].Attributes {
		if a.Value.Type() == attribute.STRING {
			if strings.Contains(a.Value.AsString(), "...[truncated]") {
				t.Fatalf("attribute %s truncated a trace payload", string(a.Key))
			}
		}
		if string(a.Key) == "trace.input" && !strings.HasSuffix(a.Value.AsString(), "PROMPT-TAIL-ZGHTEST-9d2c11\"}]}]") {
			t.Fatalf("trace.input lost payload tail; stored %d chars", len(a.Value.AsString()))
		}
	}
	t.Logf("large-payload trace.input stored %d chars, tail intact", len(bigText))
}

func findSpan(t *testing.T, stubs []tracetest.SpanStub, name string) tracetest.SpanStub {
	t.Helper()
	for _, s := range stubs {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("span %q not found", name)
	return tracetest.SpanStub{}
}

func exampleStrPtr(s string) *string { return &s }
