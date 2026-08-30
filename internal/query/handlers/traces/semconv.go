package traces

import (
	"fmt"
	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
	"regexp"
	"strings"
)

// Multi-namespace attribute aliases.
//
// We accept several semantic-convention namespaces simultaneously:
//
//   - Everstack-native (`llm.*`, `trace.*`) — what our own SDKs and agents emit.
//   - OTel GenAI (`gen_ai.*`) — the official OpenTelemetry semantic-convention.
//   - OpenInference (also under `llm.*`, but with different field names) —
//     the de-facto convention used by LangChain, LlamaIndex, Instructor, etc.
//   - Coding agents — Claude Code, Gemini CLI, OpenAI Codex. They mostly mirror
//     `gen_ai.*` on their trace spans but carry agent-specific keys (notably
//     cost, which the standards have no slot for, and bare token keys).
//
// At read-time we coalesce across all of them so a span that arrived via any
// supported SDK or coding agent lands in the same columns. New attributes get
// added in one place here, not scattered through the handlers.
var (
	modelAttrs = []string{
		"llm.model",
		"gen_ai.response.model",
		"gen_ai.request.model",
		"llm.model_name",
		"llm.request.model",
		"model", // coding agents (Claude Code / Gemini CLI / Codex) bare key
	}
	providerAttrs = []string{
		"llm.provider",
		"gen_ai.provider.name", // OTel GenAI canonical (supersedes gen_ai.system)
		"gen_ai.system",
		"provider",
		"model.provider", // gateway model-resolution span carries the resolved provider here
	}
	// providerKeyAttrs are the span-attribute keys that carry the upstream
	// provider API key id (read-side coalesce).
	providerKeyAttrs = []string{"provider.api_key_id"}
	inputTokensAttrs = []string{
		"llm.tokens.input",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.prompt_tokens",
		"llm.token_count.prompt",
		"input_tokens",       // Claude Code
		"input_token_count",  // Gemini CLI / Codex
		"agent.tokens.input", // Everstack agent runtime
	}
	outputTokensAttrs = []string{
		"llm.tokens.output",
		"gen_ai.usage.output_tokens",
		"gen_ai.usage.completion_tokens",
		"llm.token_count.completion",
		"output_tokens",       // Claude Code
		"output_token_count",  // Gemini CLI / Codex
		"agent.tokens.output", // Everstack agent runtime
	}
	totalTokensAttrs = []string{
		"llm.tokens.total",
		"gen_ai.usage.total_tokens",
		"llm.token_count.total",
		"total_tokens",
		"total_token_count",
	}
	// Cache token usage. Coding agents (Claude Code) report large
	// prompt-cache hits separately from input_tokens; without these the trace
	// would undercount the tokens actually processed.
	cacheReadTokensAttrs = []string{
		"llm.tokens.cache_read",
		"llm.tokens.cached",
		"gen_ai.usage.cache_read_input_tokens",
		"cache_read_tokens",
	}
	cacheWriteTokensAttrs = []string{
		"llm.tokens.cache_creation",
		"gen_ai.usage.cache_creation_input_tokens",
		"cache_creation_tokens",
	}
	inputAttrs = []string{
		"trace.input",
		"llm.request.messages",  // Everstack-native full message array
		"gen_ai.input.messages", // OTel GenAI semconv (structured messages)
		"gen_ai.prompt",         // OTel GenAI (legacy/string prompt)
		"input.value",           // OpenInference
	}
	outputAttrs = []string{
		"trace.output",
		"llm.response.choices",   // Everstack-native response choices
		"gen_ai.output.messages", // OTel GenAI semconv
		"gen_ai.completion",      // OTel GenAI (legacy/string completion)
		"output.value",           // OpenInference
	}
	costAttrs = []string{
		"llm.cost.total",
		"cost.estimated_usd",
		"cost_usd",          // Claude Code (the only coding agent that emits cost)
		"gen_ai.usage.cost", // forward-compat if the GenAI semconv adds a cost key
	}
	// Tool/function call name across coding-agent + GenAI conventions. Used by
	// the span-detail path to label tool spans uniformly.
	toolNameAttrs = []string{
		"gen_ai.tool.name", // OTel GenAI
		"tool.name",
		"tool_name",     // Claude Code / Codex
		"function_name", // Gemini CLI
	}
	// Session / conversation grouping across namespaces. OTel GenAI uses
	// gen_ai.conversation.id; OpenInference uses session.id. Without these, a
	// span ingested via a non-native SDK would not group into a session.
	sessionAttrs = []string{
		"trace.session_id",
		"gen_ai.conversation.id",
		"session.id",
		"session_id",
		// Agent runtime stamps agent.session.id on its spans; treat it as a
		// session source so agent runs (incl. historical, pre-trace.session_id)
		// group in the Sessions view.
		"agent.session.id",
	}
	userAttrs = []string{
		"trace.user_id",
		"user.id",
		"user_id",
	}
	// Existing ListTracesHandler filters correlation ids from this span
	// attribute; command/provider code emits it for request log-to-trace linking.
	correlationAttrs = []string{
		"correlation_id",
	}
	// TraceThreadID is emitted by the telemetry attributes registry as the
	// conversational thread id and is already used by ListTracesHandler.
	threadAttrs = []string{
		"trace.thread_id",
	}
	// Span-level (generation) I/O across namespaces. Unlike inputAttrs/outputAttrs
	// these exclude the trace-level trace.input/output keys, so a non-root
	// generation span reads its own messages, not the trace payload.
	spanInputAttrs = []string{
		"llm.request.messages",  // Everstack-native
		"gen_ai.input.messages", // OTel GenAI
		"gen_ai.prompt",         // OTel GenAI (legacy/string)
		"input.value",           // OpenInference
	}
	spanOutputAttrs = []string{
		"llm.response.choices",   // Everstack-native
		"gen_ai.output.messages", // OTel GenAI
		"gen_ai.completion",      // OTel GenAI (legacy/string)
		"output.value",           // OpenInference
	}
)

// coalesceString returns a SQL fragment that evaluates to the first non-empty
// string attribute among the given keys, falling back to ”.
//
//	coalesceString("SpanAttributes", "llm.model", "gen_ai.response.model")
//	→ coalesce(nullIf(SpanAttributes['llm.model'], ''), nullIf(SpanAttributes['gen_ai.response.model'], ''), '')
func coalesceString(mapName string, attrs []string) string {
	parts := make([]string, 0, len(attrs)+1)
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("nullIf(%s['%s'], '')", mapName, a))
	}
	parts = append(parts, "''")
	return "coalesce(" + strings.Join(parts, ", ") + ")"
}

// rootPreferred returns an aggregate that reads a field from the root span when
// there is one, and otherwise from any span in the trace that carries it.
//
// The root-derived columns were a plain maxIf scoped to the root span, which
// evaluates to the empty string for a trace whose root has not been emitted
// yet. Roots are emitted when the work finishes, so every in-flight trace showed
// blank user, session, thread, model-parameter and metadata columns until it
// completed. Those attributes are carried on child spans too, so falling back to
// any non-empty value fills the row immediately while still preferring the
// root's own value once it lands.
//
// expr is evaluated more than once, so it must not contain bound placeholders.
// Every fragment in this file inlines its attribute names, which is what makes
// that safe — do not pass a caller-supplied string with a `?` in it.
func rootPreferred(expr string) string {
	return fmt.Sprintf(
		"coalesce(nullIf(maxIf(%s, ParentSpanId = ''), ''), nullIf(maxIf(%s, %s != ''), ''), '')",
		expr, expr, expr,
	)
}

// coalesceFloat returns a SQL fragment that evaluates to the first non-zero
// numeric attribute among the given keys, falling back to 0.
func coalesceFloat(mapName string, attrs []string) string {
	parts := make([]string, 0, len(attrs)+1)
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("nullIf(toFloat64OrZero(%s['%s']), 0)", mapName, a))
	}
	parts = append(parts, "0")
	return "coalesce(" + strings.Join(parts, ", ") + ")"
}

// coalesceInt64 returns a SQL fragment that evaluates to the first non-zero
// integer attribute among the given keys, falling back to 0.
func coalesceInt64(mapName string, attrs []string) string {
	parts := make([]string, 0, len(attrs)+1)
	for _, a := range attrs {
		parts = append(parts, fmt.Sprintf("nullIf(toInt64OrZero(%s['%s']), 0)", mapName, a))
	}
	parts = append(parts, "0")
	return "coalesce(" + strings.Join(parts, ", ") + ")"
}

// safeAttrKey bounds a span-attribute key to characters that can appear in an
// OTel attribute name. Tenant-supplied semantic-mapping keys are inlined into
// the coalesce SQL, so anything that could escape a string literal is rejected.
var safeAttrKey = regexp.MustCompile(`^[a-zA-Z0-9_.:/\-]{1,128}$`)

// withExtra appends tenant-supplied extra keys to a default key list. Defaults
// keep priority (they come first), so a tenant mapping is a fallback for spans
// that do not carry one of our known keys. Unsafe keys are dropped defensively.
func withExtra(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	for _, e := range extra {
		if safeAttrKey.MatchString(e) {
			out = append(out, e)
		}
	}
	return out
}

// The *SQL fragments accept optional tenant-supplied extra attribute keys so a
// tenant can alias its own attribute names into our typed fields (semantic
// mappings) without us editing this file. Existing callers pass none.

// modelSQL is the canonical fragment for a span's model name.
func modelSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(modelAttrs, extra))
}

// providerSQL is the canonical fragment for a span's provider/system.
func providerSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(providerAttrs, extra))
}

// isGenerationSpanSQL matches an actual LLM-call (generation) span. Provider
// calls are named `provider.<p>.chat`; OTel-GenAI generation spans set
// observation.type=GENERATION. Non-generation spans (gateway model-resolution,
// agent turn) can still carry a stale provider/model from a failed first
// attempt, so trace-level rollups must ignore them.
const isGenerationSpanSQL = "(SpanName LIKE 'provider.%' OR SpanAttributes['observation.type'] = 'GENERATION')"

// latestSuccessfulGenExpr builds a ClickHouse expression that reads a value
// (provider or model) from the LATEST SUCCESSFUL generation span in a trace,
// falling back to the latest generation span, then to any span. It fixes the
// multi-provider attribution bug: a failed first attempt (e.g. Cohere) stamped
// provider/model onto its resolution/turn spans and polluted the old
// `anyIf(frag != ”)` rollup, so the list showed Cohere while the detail
// (which restricts to generation spans) correctly showed the later OpenAI call.
//
// provider and model must be read with the SAME condition and ordering so they
// always resolve to the same span; callers pass frag=providerFrag for provider
// and frag=modelFrag for model, and the same providerFrag/modelFrag pair for the
// shared "has attribution" guard. Ordering by (Timestamp, SpanId) is total, so
// the two argMaxIf calls pick an identical row.
func latestSuccessfulGenExpr(frag, providerFrag, modelFrag string) string {
	hasAttr := fmt.Sprintf("(%s != '' OR %s != '')", providerFrag, modelFrag)
	okCond := fmt.Sprintf("%s AND %s AND %s", isGenerationSpanSQL, otelstatus.IsNotError(otelstatus.Column), hasAttr)
	anyGenCond := fmt.Sprintf("%s AND %s", isGenerationSpanSQL, hasAttr)
	const order = "(Timestamp, SpanId)"
	return fmt.Sprintf(
		"coalesce(nullIf(argMaxIf(%[1]s, %[4]s, %[2]s), ''), nullIf(argMaxIf(%[1]s, %[4]s, %[3]s), ''), nullIf(anyIf(%[1]s, %[1]s != ''), ''), '')",
		frag, okCond, anyGenCond, order,
	)
}

// costSQL is the canonical fragment for a span's total LLM cost in USD.
func costSQL(extra ...string) string {
	return coalesceFloat("SpanAttributes", withExtra(costAttrs, extra))
}

// inputTokensSQL / outputTokensSQL / totalTokensSQL for usage.
func inputTokensSQL(extra ...string) string {
	return coalesceInt64("SpanAttributes", withExtra(inputTokensAttrs, extra))
}
func outputTokensSQL(extra ...string) string {
	return coalesceInt64("SpanAttributes", withExtra(outputTokensAttrs, extra))
}
func cacheReadTokensSQL(extra ...string) string {
	return coalesceInt64("SpanAttributes", withExtra(cacheReadTokensAttrs, extra))
}
func cacheWriteTokensSQL(extra ...string) string {
	return coalesceInt64("SpanAttributes", withExtra(cacheWriteTokensAttrs, extra))
}

// totalTokensSQL prefers an explicit total key; when none is present (coding
// agents report input/output/cache separately and never a total) it falls back
// to the sum of the components so the trace still reports the tokens processed.
func totalTokensSQL(extra ...string) string {
	base := coalesceInt64("SpanAttributes", withExtra(totalTokensAttrs, extra))
	sum := fmt.Sprintf("(%s + %s + %s + %s)",
		inputTokensSQL(), outputTokensSQL(), cacheReadTokensSQL(), cacheWriteTokensSQL())
	return fmt.Sprintf("if(%s > 0, %s, %s)", base, base, sum)
}

// traceInputSQL / traceOutputSQL for the request/response payload aliases.
func traceInputSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(inputAttrs, extra))
}
func traceOutputSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(outputAttrs, extra))
}

// sessionSQL / userSQL are the canonical fragments for a span's session and
// user, coalesced across Everstack-native, OTel GenAI, and OpenInference keys so
// spans ingested via any SDK group correctly.
func sessionSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(sessionAttrs, extra))
}
func userSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(userAttrs, extra))
}

// toolNameSQL is the canonical fragment for a tool/function-call span's name.
func toolNameSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(toolNameAttrs, extra))
}

// correlationSQL / threadSQL preserve the existing direct map lookup used by
// ListTracesHandler's hand-written membership filters.
func correlationSQL() string {
	return fmt.Sprintf("SpanAttributes['%s']", correlationAttrs[0])
}
func threadSQL() string {
	return fmt.Sprintf("SpanAttributes['%s']", threadAttrs[0])
}

// ttftAttrs: time-to-first-token in ms across streaming conventions. Keys
// verified in internal/telemetry/attributes/registry.go (LLMStreamTimeToFirstTokenMs,
// LLMStreamFirstChunkLatencyMs, LatencyTTFTMs).
var ttftAttrs = []string{
	"llm.stream.time_to_first_token_ms",
	"llm.stream.first_chunk_latency_ms",
	"latency.ttft_ms",
}

// ttftSQL is the raw (string) coalesce for time-to-first-token; numeric callers
// wrap it in toInt64OrZero.
func ttftSQL(extra ...string) string {
	return coalesceString("SpanAttributes", withExtra(ttftAttrs, extra))
}

// toolErrorExistsSQL matches a span where a tool call failed. Key verified in
// registry.go (AgentToolCallSuccess = "agent.tool_call.success").
func toolErrorExistsSQL() string {
	return "SpanAttributes['agent.tool_call.success'] = 'false'"
}

// cacheHitExistsSQL matches a span served from cache. Key verified in registry.go
// (CacheHit = "cache.hit", a bool string).
func cacheHitExistsSQL() string {
	return "SpanAttributes['cache.hit'] = 'true'"
}

// agentNameSQL is the fragment for an agent span's name. Key verified in
// registry.go (AgentName = "agent.name").
func agentNameSQL() string {
	return "SpanAttributes['agent.name']"
}

// hasSpanCondition maps a "has:<span-type>" value to a fixed span-match
// condition. Values map to observation.type (observation.go taxonomy) or a
// SpanName pattern; the value is never interpolated, so this is injection-safe.
func hasSpanCondition(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sandbox":
		return "SpanAttributes['observation.type'] = 'SANDBOX'", true
	case "tool":
		return "SpanAttributes['observation.type'] = 'TOOL'", true
	case "agent":
		return "SpanAttributes['observation.type'] = 'AGENT'", true
	case "browser":
		return "SpanAttributes['observation.type'] = 'BROWSER'", true
	case "voice", "media", "audio":
		return "SpanAttributes['observation.type'] = 'MEDIA'", true
	case "memory":
		return "SpanAttributes['observation.type'] = 'RETRIEVER'", true
	case "vector", "embedding", "embeddings":
		return "SpanAttributes['observation.type'] = 'EMBEDDING'", true
	case "llm", "generation":
		return "SpanAttributes['observation.type'] = 'GENERATION'", true
	case "mcp":
		return "SpanName LIKE 'mcp.%'", true
	default:
		return "", false
	}
}

// AttrFromMap returns the first non-empty value among the given keys in a
// Go-side attribute map. Mirrors the SQL coalesceString helper for use in
// trace_transformer and similar.
func AttrFromMap(attrs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := attrs[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// ModelFromAttrs returns the model name across all supported semconvs.
func ModelFromAttrs(attrs map[string]string) string {
	return AttrFromMap(attrs, modelAttrs...)
}

// ProviderFromAttrs returns the provider name across all supported semconvs,
// falling back to model-name inference when no provider attribute is present.
// Coding agents pointed at alternative model endpoints (e.g. Claude Code talking
// to GLM via Z.ai or Kimi via Moonshot over an Anthropic-compatible endpoint)
// often carry no provider attribute — only the model name reveals the provider.
func ProviderFromAttrs(attrs map[string]string) string {
	if p := AttrFromMap(attrs, providerAttrs...); p != "" {
		return p
	}
	return InferProviderFromModel(ModelFromAttrs(attrs))
}

// ToolNameFromAttrs returns a tool/function-call span's tool name across the
// coding-agent and GenAI conventions.
func ToolNameFromAttrs(attrs map[string]string) string {
	return AttrFromMap(attrs, toolNameAttrs...)
}

// InferProviderFromModel maps a model name to its provider when no provider
// attribute is available. Conservative prefix matching; returns "" when unknown
// so callers keep whatever provider attribute they already had.
func InferProviderFromModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "":
		return ""
	case strings.HasPrefix(m, "glm"):
		return "zhipu"
	case strings.HasPrefix(m, "kimi"), strings.HasPrefix(m, "moonshot"):
		return "moonshot"
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return "openai"
	case strings.HasPrefix(m, "gemini"):
		return "google"
	case strings.HasPrefix(m, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(m, "mistral"), strings.HasPrefix(m, "mixtral"), strings.HasPrefix(m, "magistral"), strings.HasPrefix(m, "codestral"):
		return "mistral"
	case strings.HasPrefix(m, "llama"):
		return "meta"
	case strings.HasPrefix(m, "grok"):
		return "xai"
	case strings.HasPrefix(m, "qwen"):
		return "alibaba"
	case strings.HasPrefix(m, "command"):
		return "cohere"
	}
	return ""
}

// SessionFromAttrs returns the session/conversation id across all supported
// semconvs (Everstack trace.session_id, OTel gen_ai.conversation.id,
// OpenInference session.id).
func SessionFromAttrs(attrs map[string]string) string {
	return AttrFromMap(attrs, sessionAttrs...)
}

// UserFromAttrs returns the end-user id across all supported semconvs.
func UserFromAttrs(attrs map[string]string) string {
	return AttrFromMap(attrs, userAttrs...)
}

// SpanInputFromAttrs returns a generation span's input messages across semconvs.
func SpanInputFromAttrs(attrs map[string]string) string {
	return AttrFromMap(attrs, spanInputAttrs...)
}

// SpanOutputFromAttrs returns a generation span's output across semconvs.
func SpanOutputFromAttrs(attrs map[string]string) string {
	return AttrFromMap(attrs, spanOutputAttrs...)
}

// splitMetadataPredicate parses a "key=value" string into key + value parts.
// Returns ok=false if the predicate is malformed or empty.
func splitMetadataPredicate(pred string) (key string, value string, ok bool) {
	for i := 0; i < len(pred); i++ {
		if pred[i] == '=' {
			k := pred[:i]
			v := pred[i+1:]
			if k == "" {
				return "", "", false
			}
			return k, v, true
		}
	}
	return "", "", false
}
