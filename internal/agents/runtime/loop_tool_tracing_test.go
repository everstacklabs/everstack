package runtime

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type tracedSyntheticTool struct {
	spanID trace.SpanID
}

func (t *tracedSyntheticTool) ExecuteSyntheticTool(ctx context.Context, _, _ string) (string, error) {
	t.spanID = trace.SpanContextFromContext(ctx).SpanID()
	_, sandboxSpan := telemetry.StartSandboxExecSpan(ctx, "sandbox-1", "node")
	sandboxSpan.End()
	return `{"ok":true}`, nil
}

func TestExecuteTracedSyntheticToolNestsSandboxSpanUnderToolSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := telemetry.GetGlobalTracerProvider()
	telemetry.SetGlobalTracerProvider(tp)
	t.Cleanup(func() {
		telemetry.SetGlobalTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	turnCtx, turnSpan := telemetry.StartAgentTurnSpan(context.Background(), "session-1", 1)
	handler := &tracedSyntheticTool{}
	result, err, _ := executeTracedSyntheticTool(
		turnCtx,
		handler,
		"call-1",
		"sandbox_execute",
		`{"language":"javascript","code":"console.log(1)"}`,
	)
	turnSpan.End()

	if err != nil {
		t.Fatalf("executeTracedSyntheticTool() error = %v", err)
	}
	if result != `{"ok":true}` {
		t.Fatalf("executeTracedSyntheticTool() result = %q", result)
	}

	spans := make(map[string]tracetest.SpanStub)
	for _, span := range exporter.GetSpans() {
		spans[span.Name] = span
	}

	toolSpan, ok := spans["agent.tool.sandbox_execute"]
	if !ok {
		t.Fatalf("agent tool span missing; got spans %v", spanNames(spans))
	}
	sandboxSpan, ok := spans["sandbox.exec.node"]
	if !ok {
		t.Fatalf("sandbox execution span missing; got spans %v", spanNames(spans))
	}
	turn, ok := spans["agent.turn.1"]
	if !ok {
		t.Fatalf("agent turn span missing; got spans %v", spanNames(spans))
	}

	if toolSpan.Parent.SpanID() != turn.SpanContext.SpanID() {
		t.Errorf("tool parent = %s, want turn span %s", toolSpan.Parent.SpanID(), turn.SpanContext.SpanID())
	}
	if sandboxSpan.Parent.SpanID() != toolSpan.SpanContext.SpanID() {
		t.Errorf("sandbox parent = %s, want tool span %s", sandboxSpan.Parent.SpanID(), toolSpan.SpanContext.SpanID())
	}
	if handler.spanID != toolSpan.SpanContext.SpanID() {
		t.Errorf("handler context span = %s, want tool span %s", handler.spanID, toolSpan.SpanContext.SpanID())
	}

	got := map[string]interface{}{}
	for _, attribute := range toolSpan.Attributes {
		got[string(attribute.Key)] = attribute.Value.AsInterface()
	}
	if got[attrs.ObservationType] != string(telemetry.ObservationTypeTool) {
		t.Errorf("observation.type = %v, want TOOL", got[attrs.ObservationType])
	}
	if got[attrs.AgentToolCallSuccess] != true {
		t.Errorf("agent.tool_call.success = %v, want true", got[attrs.AgentToolCallSuccess])
	}
}

func spanNames(spans map[string]tracetest.SpanStub) []string {
	names := make([]string, 0, len(spans))
	for name := range spans {
		names = append(names, name)
	}
	return names
}
