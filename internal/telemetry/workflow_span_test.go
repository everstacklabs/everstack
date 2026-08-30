package telemetry

import (
	"context"
	"strings"
	"testing"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestEmitterSpanNamesExcludeLifecycle is the M1-T11 denylist guard: it exercises
// every emitter helper and asserts none produce an operational/lifecycle event
// name, and that every emitted span carries a known semantic prefix. This keeps
// the "instrument semantic operations, not lifecycle" boundary honest.
func TestEmitterSpanNamesExcludeLifecycle(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	ctx := context.Background()
	_, s := StartWorkflowRunSpan(ctx, "w", "r", "t")
	s.End()
	_, s = StartWorkflowNodeSpan(ctx, "n", "provider", "x")
	s.End()
	_, s = StartMemorySpan(ctx, "retrieve", "a")
	s.End()
	_, s = StartSandboxFSSpan(ctx, "write", "sb")
	s.End()
	_, s = StartSandboxExecSpan(ctx, "sb", "git")
	s.End()
	_, s = StartHarnessRunSpan(ctx, "t")
	s.End()
	_, s = StartBrowserSpan(ctx, "navigate")
	s.End()
	_, s = StartMCPToolSpan(ctx, "srv", "tool")
	s.End()
	_, s = StartA2ACallSpan(ctx, "host")
	s.End()
	_, s = StartMemoryEmbeddingSpan(ctx, "m")
	s.End()
	_, s = StartVectorStoreSpan(ctx, "query", "pgvector")
	s.End()
	_, s = StartScorerSpan(ctx, "task_completion")
	s.End()

	denylist := map[string]bool{
		"sandbox.create": true, "sandbox.ready": true, "sandbox.destroy": true,
		"session.start": true, "session.end": true, "turn.start": true,
		"turn.end": true, "approval.requested": true, "user_input.requested": true,
	}
	allowed := []string{"workflow.", "memory.", "sandbox.", "harness.", "browser.", "mcp.", "a2a.", "embedding.", "vector.", "scorer."}
	for _, sp := range exporter.GetSpans() {
		if denylist[sp.Name] {
			t.Errorf("emitter produced an operational/lifecycle span name: %q", sp.Name)
		}
		ok := false
		for _, p := range allowed {
			if strings.HasPrefix(sp.Name, p) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("span %q has no known semantic prefix", sp.Name)
		}
	}
}

func TestRecordGuardrailCheck(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	// Block case: violations present.
	ctx, span := tp.Tracer("t").Start(context.Background(), "host")
	RecordGuardrailCheck(ctx, "guardrail.input", []string{"pii: detected SSN", "prompt_injection: override"})
	// Pass case: no violations.
	RecordGuardrailCheck(ctx, "guardrail.output", nil)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	events := spans[0].Events
	if len(events) != 2 {
		t.Fatalf("expected 2 guardrail events, got %d", len(events))
	}
	get := func(idx int) map[string]string {
		m := map[string]string{}
		for _, a := range events[idx].Attributes {
			m[string(a.Key)] = a.Value.AsString()
		}
		return m
	}
	in := get(0)
	if events[0].Name != "guardrail.input" || in[attrs.GuardrailResult] != "block" {
		t.Errorf("input event = %q result=%q, want guardrail.input/block", events[0].Name, in[attrs.GuardrailResult])
	}
	if in[attrs.GuardrailRule] != "pii, prompt_injection" {
		t.Errorf("rule = %q, want 'pii, prompt_injection'", in[attrs.GuardrailRule])
	}
	out := get(1)
	if events[1].Name != "guardrail.output" || out[attrs.GuardrailResult] != "pass" {
		t.Errorf("output event = %q result=%q, want guardrail.output/pass", events[1].Name, out[attrs.GuardrailResult])
	}
}

func TestStartScorerSpanJoinsTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	const traceHex = "0123456789abcdef0123456789abcdef"
	ctx := ScorerTraceContext(context.Background(), traceHex)
	_, span := StartScorerSpan(ctx, "task_completion")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "scorer.task_completion" {
		t.Fatalf("expected scorer.task_completion span, got %+v", spans)
	}
	// The scorer span must join the original trace.
	if got := spans[0].SpanContext.TraceID().String(); got != traceHex {
		t.Errorf("scorer span trace id = %q, want %q", got, traceHex)
	}
	got := map[string]string{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeScorer) {
		t.Errorf("observation.type = %q, want SCORER", got[attrs.ObservationType])
	}
	if got[attrs.ObservationPurpose] != string(PurposeScorer) {
		t.Errorf("purpose = %q, want scorer", got[attrs.ObservationPurpose])
	}
	if got[attrs.ScorerName] != "task_completion" {
		t.Errorf("scorer.name = %q", got[attrs.ScorerName])
	}
}

func TestScorerTraceContextInvalid(t *testing.T) {
	// An invalid trace id leaves the context unchanged (no panic, no join).
	ctx := ScorerTraceContext(context.Background(), "not-a-trace-id")
	if ctx == nil {
		t.Fatal("nil context returned")
	}
}

func TestStartMCPToolSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartMCPToolSpan(context.Background(), "srv-1", "search")
	span.End()
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "mcp.tool.call" {
		t.Fatalf("expected mcp.tool.call span, got %+v", spans)
	}
	got := map[string]string{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeTool) {
		t.Errorf("observation.type = %q, want TOOL", got[attrs.ObservationType])
	}
	if got[attrs.MCPServerID] != "srv-1" || got[attrs.MCPToolName] != "search" {
		t.Errorf("mcp attrs wrong: %+v", got)
	}
}

func TestStartA2ACallSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartA2ACallSpan(context.Background(), "peer.example.com")
	span.End()
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "a2a.call" {
		t.Fatalf("expected a2a.call span, got %+v", spans)
	}
	got := map[string]string{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeAgent) {
		t.Errorf("observation.type = %q, want AGENT", got[attrs.ObservationType])
	}
	if got[attrs.A2ATarget] != "peer.example.com" {
		t.Errorf("a2a.target = %q", got[attrs.A2ATarget])
	}
}

func TestStartWorkflowNodeSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartWorkflowNodeSpan(context.Background(), "n1", "provider", "LLM Call")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name != "workflow.node.provider" {
		t.Errorf("span name = %q, want workflow.node.provider", s.Name)
	}
	got := map[string]string{}
	for _, a := range s.Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeGeneration) {
		t.Errorf("observation.type = %q, want GENERATION", got[attrs.ObservationType])
	}
	if got[attrs.NodeID] != "n1" || got[attrs.NodeType] != "provider" || got[attrs.NodeName] != "LLM Call" {
		t.Errorf("node attrs wrong: %+v", got)
	}
}

func TestStartMemorySpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartMemorySpan(context.Background(), "retrieve", "agent-1")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name != "memory.retrieve" {
		t.Errorf("span name = %q, want memory.retrieve", s.Name)
	}
	got := map[string]string{}
	for _, a := range s.Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeRetriever) {
		t.Errorf("observation.type = %q, want RETRIEVER", got[attrs.ObservationType])
	}
	if got[attrs.AgentID] != "agent-1" || got[attrs.MemoryOperation] != "retrieve" {
		t.Errorf("memory attrs wrong: %+v", got)
	}
}

func TestStartBrowserSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartBrowserSpan(context.Background(), "navigate")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "browser.navigate" {
		t.Fatalf("expected browser.navigate span, got %+v", spans)
	}
	got := map[string]string{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeBrowser) {
		t.Errorf("observation.type = %q, want BROWSER", got[attrs.ObservationType])
	}
	if got[attrs.BrowserAction] != "navigate" {
		t.Errorf("browser.action = %q, want navigate", got[attrs.BrowserAction])
	}
}

func TestStartSandboxFSSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartSandboxFSSpan(context.Background(), "write", "sb-1")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "sandbox.fs.write" {
		t.Fatalf("expected sandbox.fs.write span, got %+v", spans)
	}
	got := map[string]string{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeSandbox) {
		t.Errorf("observation.type = %q, want SANDBOX", got[attrs.ObservationType])
	}
	if got[attrs.SandboxID] != "sb-1" {
		t.Errorf("sandbox.id = %q, want sb-1", got[attrs.SandboxID])
	}
}

func TestStartHarnessRunSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartHarnessRunSpan(context.Background(), "tenant-x")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "harness.run" {
		t.Fatalf("expected harness.run span, got %+v", spans)
	}
	got := map[string]string{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.ObservationType] != string(ObservationTypeHarness) {
		t.Errorf("observation.type = %q, want HARNESS", got[attrs.ObservationType])
	}
	if got[attrs.RootType] != string(RootTypeHarness) || got[attrs.TenantID] != "tenant-x" {
		t.Errorf("harness attrs wrong: %+v", got)
	}
}

func TestStartWorkflowRunSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	_, span := StartWorkflowRunSpan(context.Background(), "wf1", "run1", "tenant1")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	s := spans[0]
	if s.Name != "workflow.run" {
		t.Errorf("span name = %q, want workflow.run", s.Name)
	}
	got := map[string]string{}
	for _, a := range s.Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	if got[attrs.RootType] != string(RootTypeWorkflow) {
		t.Errorf("root_type = %q, want workflow", got[attrs.RootType])
	}
	if got[attrs.RunID] != "run1" || got[attrs.WorkflowID] != "wf1" || got[attrs.TenantID] != "tenant1" {
		t.Errorf("run attrs wrong: %+v", got)
	}
}
