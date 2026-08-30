package telemetry

import "testing"

func TestDetermineObservationTypeFromSpanName(t *testing.T) {
	cases := map[string]ObservationType{
		"agent.tool.search":      ObservationTypeTool,
		"agent.tool_call.run":    ObservationTypeTool,
		"agent.turn.3":           ObservationTypeAgent,
		"agent.session":          ObservationTypeAgent,
		"provider.openai.chat":   ObservationTypeGeneration,
		"embedding.create":       ObservationTypeEmbedding,
		"vector.query":           ObservationTypeRetriever,
		"memory.retrieve":        ObservationTypeRetriever,
		"cache.lookup":           ObservationTypeCache,
		"sandbox.exec":           ObservationTypeSandbox,
		"browser.navigate":       ObservationTypeBrowser,
		"computer.click":         ObservationTypeComputer,
		"guardrail.pii":          ObservationTypeGuardrail,
		"scorer.helpfulness":     ObservationTypeScorer,
		"facet.sentiment":        ObservationTypeScorer,
		"workflow.run":           ObservationTypeWorkflow,
		"node.start":             ObservationTypeWorkflow,
		"router.decide":          ObservationTypeControl,
		"http.get":               ObservationTypeHTTP,
		"webhook.post":           ObservationTypeHTTP,
		"integration.github":     ObservationTypeIntegration,
		"harness.exec":           ObservationTypeHarness,
		"adk.run":                ObservationTypeHarness,
		"mcp.tool.call":          ObservationTypeTool,
		"a2a.call":               ObservationTypeAgent,
		"tts.synthesize":         ObservationTypeMedia,
		"stream.first_chunk":     ObservationTypeEvent,
		"fallback.attempt":       ObservationTypeSpan,
		"gateway.chat":           ObservationTypeSpan,
		"something.unrecognized": ObservationTypeSpan,
	}
	for name, want := range cases {
		if got := DetermineObservationTypeFromSpanName(name); got != want {
			t.Errorf("DetermineObservationTypeFromSpanName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestWorkflowNodeTypeToObservationType(t *testing.T) {
	// Every one of the 19 Studio node types must map to a known type.
	cases := map[string]ObservationType{
		"start":            ObservationTypeWorkflow,
		"response":         ObservationTypeWorkflow,
		"provider":         ObservationTypeGeneration,
		"cache":            ObservationTypeCache,
		"memory":           ObservationTypeRetriever,
		"agent":            ObservationTypeAgent,
		"function":         ObservationTypeTool,
		"inputGuardrails":  ObservationTypeGuardrail,
		"outputGuardrails": ObservationTypeGuardrail,
		"tts":              ObservationTypeMedia,
		"stt":              ObservationTypeMedia,
		"voiceClone":       ObservationTypeMedia,
		"httpRequest":      ObservationTypeHTTP,
		"webhook":          ObservationTypeHTTP,
		"ifElse":           ObservationTypeControl,
		"router":           ObservationTypeControl,
		"loadBalancer":     ObservationTypeControl,
		"auth":             ObservationTypeControl,
		"rateLimiter":      ObservationTypeControl,
	}
	for nodeType, want := range cases {
		if got := WorkflowNodeTypeToObservationType(nodeType); got != want {
			t.Errorf("WorkflowNodeTypeToObservationType(%q) = %q, want %q", nodeType, got, want)
		}
	}
	if got := WorkflowNodeTypeToObservationType("totallyUnknown"); got != ObservationTypeWorkflow {
		t.Errorf("unknown node type = %q, want WORKFLOW", got)
	}
}

func TestNormalizeObservationType(t *testing.T) {
	cases := map[string]ObservationType{
		"llm":         ObservationTypeLLM,
		"LLM":         ObservationTypeLLM,
		"  sandbox  ": ObservationTypeSandbox,
		"GENERATION":  ObservationTypeGeneration,
		"bogus":       ObservationTypeSpan,
		"":            ObservationTypeSpan,
	}
	for in, want := range cases {
		if got := NormalizeObservationType(in); got != want {
			t.Errorf("NormalizeObservationType(%q) = %q, want %q", in, got, want)
		}
	}
}
