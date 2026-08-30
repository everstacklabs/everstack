package traces

import "testing"

func TestAttrFromMapPrecedence(t *testing.T) {
	attrs := map[string]string{"a": "", "b": "second", "c": "third"}
	if got := AttrFromMap(attrs, "a", "b", "c"); got != "second" {
		t.Errorf("AttrFromMap = %q, want 'second' (skips empty, first non-empty wins)", got)
	}
	if got := AttrFromMap(attrs, "missing"); got != "" {
		t.Errorf("AttrFromMap(missing) = %q, want empty", got)
	}
}

func TestModelAndProviderAcrossSemconvs(t *testing.T) {
	cases := []struct {
		name         string
		attrs        map[string]string
		wantModel    string
		wantProvider string
	}{
		{
			"everstack-native",
			map[string]string{"llm.model": "gpt-4o", "llm.provider": "openai"},
			"gpt-4o", "openai",
		},
		{
			"otel-genai",
			map[string]string{"gen_ai.response.model": "claude-3", "gen_ai.system": "anthropic"},
			"claude-3", "anthropic",
		},
		{
			"openinference",
			map[string]string{"llm.model_name": "mistral-large", "provider": "mistral"},
			"mistral-large", "mistral",
		},
	}
	for _, c := range cases {
		if got := ModelFromAttrs(c.attrs); got != c.wantModel {
			t.Errorf("%s: ModelFromAttrs = %q, want %q", c.name, got, c.wantModel)
		}
		if got := ProviderFromAttrs(c.attrs); got != c.wantProvider {
			t.Errorf("%s: ProviderFromAttrs = %q, want %q", c.name, got, c.wantProvider)
		}
	}
}

func TestSessionAndUserAcrossSemconvs(t *testing.T) {
	// OTel GenAI conversation id must group as a session.
	if got := SessionFromAttrs(map[string]string{"gen_ai.conversation.id": "conv-1"}); got != "conv-1" {
		t.Errorf("gen_ai.conversation.id session = %q, want conv-1", got)
	}
	// OpenInference session.id.
	if got := SessionFromAttrs(map[string]string{"session.id": "sess-2"}); got != "sess-2" {
		t.Errorf("session.id session = %q, want sess-2", got)
	}
	// Native takes precedence when multiple are present.
	if got := SessionFromAttrs(map[string]string{
		"trace.session_id": "native", "gen_ai.conversation.id": "conv",
	}); got != "native" {
		t.Errorf("precedence: session = %q, want native", got)
	}
	if got := UserFromAttrs(map[string]string{"user.id": "u-1"}); got != "u-1" {
		t.Errorf("user.id user = %q, want u-1", got)
	}
}

func TestSpanIOAcrossSemconvs(t *testing.T) {
	// OTel-GenAI generation span input/output.
	if got := SpanInputFromAttrs(map[string]string{"gen_ai.input.messages": "[in]"}); got != "[in]" {
		t.Errorf("gen_ai input = %q, want [in]", got)
	}
	if got := SpanOutputFromAttrs(map[string]string{"gen_ai.output.messages": "[out]"}); got != "[out]" {
		t.Errorf("gen_ai output = %q, want [out]", got)
	}
	// OpenInference.
	if got := SpanInputFromAttrs(map[string]string{"input.value": "oi-in"}); got != "oi-in" {
		t.Errorf("openinference input = %q, want oi-in", got)
	}
	// Native precedence.
	if got := SpanInputFromAttrs(map[string]string{
		"llm.request.messages": "native", "gen_ai.input.messages": "otel",
	}); got != "native" {
		t.Errorf("precedence input = %q, want native", got)
	}
	// Span-level I/O must NOT pull the trace-level payload key.
	if got := SpanInputFromAttrs(map[string]string{"trace.input": "trace-payload"}); got != "" {
		t.Errorf("span input wrongly read trace.input: %q", got)
	}
}

func TestModelFromAttrs_codingAgents(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]string
		want string
	}{
		{"everstack llm.model", map[string]string{"llm.model": "gpt-4o"}, "gpt-4o"},
		{"gen_ai response model wins over request", map[string]string{"gen_ai.request.model": "claude-opus-4-6", "gen_ai.response.model": "claude-opus-4-6-20260101"}, "claude-opus-4-6-20260101"},
		{"claude code bare model fallback", map[string]string{"model": "claude-sonnet-4-6"}, "claude-sonnet-4-6"},
		{"none", map[string]string{"foo": "bar"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelFromAttrs(c.in); got != c.want {
				t.Fatalf("ModelFromAttrs = %q, want %q", got, c.want)
			}
		})
	}
}

func TestProviderFromAttrs_explicitWinsOverInference(t *testing.T) {
	// An explicit provider attribute always wins over model-name inference.
	in := map[string]string{"gen_ai.system": "anthropic", "model": "glm-4.7"}
	if got := ProviderFromAttrs(in); got != "anthropic" {
		t.Fatalf("ProviderFromAttrs = %q, want anthropic (explicit attr wins)", got)
	}
}

func TestProviderFromAttrs_inferenceFallback(t *testing.T) {
	// No provider attribute -> infer from the model name. This is the GLM/Kimi
	// case: a coding agent pointed at an alternative endpoint emits only a model.
	cases := []struct {
		model string
		want  string
	}{
		{"glm-4.7", "zhipu"},
		{"kimi-k2.7-code", "moonshot"},
		{"moonshot-v1-128k", "moonshot"},
		{"claude-opus-4-6", "anthropic"},
		{"gpt-4o", "openai"},
		{"o3-mini", "openai"},
		{"gemini-2.5-pro", "google"},
		{"deepseek-chat", "deepseek"},
		{"qwen-2.5-coder", "alibaba"},
		{"grok-3", "xai"},
		{"some-unknown-model", ""},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			got := ProviderFromAttrs(map[string]string{"model": c.model})
			if got != c.want {
				t.Fatalf("ProviderFromAttrs(model=%q) = %q, want %q", c.model, got, c.want)
			}
			if direct := InferProviderFromModel(c.model); direct != c.want {
				t.Fatalf("InferProviderFromModel(%q) = %q, want %q", c.model, direct, c.want)
			}
		})
	}
}

func TestToolNameFromAttrs(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]string
		want string
	}{
		{"gen_ai", map[string]string{"gen_ai.tool.name": "read_file"}, "read_file"},
		{"claude code / codex", map[string]string{"tool_name": "Bash"}, "Bash"},
		{"gemini cli", map[string]string{"function_name": "run_shell_command"}, "run_shell_command"},
		{"none", map[string]string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ToolNameFromAttrs(c.in); got != c.want {
				t.Fatalf("ToolNameFromAttrs = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAgentTokenAttrsRegistered guards that the coding-agent token keys are part
// of the coalesce lists (so they flow into the token SQL fragments).
func TestAgentTokenAttrsRegistered(t *testing.T) {
	wantIn := []string{"input_tokens", "input_token_count", "agent.tokens.input", "gen_ai.usage.input_tokens"}
	for _, k := range wantIn {
		if !contains(inputTokensAttrs, k) {
			t.Errorf("inputTokensAttrs missing %q", k)
		}
	}
	wantOut := []string{"output_tokens", "output_token_count", "agent.tokens.output"}
	for _, k := range wantOut {
		if !contains(outputTokensAttrs, k) {
			t.Errorf("outputTokensAttrs missing %q", k)
		}
	}
	if !contains(costAttrs, "cost_usd") {
		t.Errorf("costAttrs missing cost_usd (Claude Code cost would not render)")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
