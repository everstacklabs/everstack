package telemetry

import (
	"strings"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ObservationType is the semantic span taxonomy (traces-module-replan section 5).
// One type system spans every execution surface: a node looks the same in a trace
// whether it ran inside a workflow or an agent. The three legacy Langfuse types
// (SPAN, GENERATION, EVENT) are retained for compatibility; GENERATION is the
// Langfuse-compatible spelling of an LLM call and aliases ObservationTypeLLM.
type ObservationType string

const (
	// Legacy / Langfuse-compatible
	ObservationTypeSpan       ObservationType = "SPAN"
	ObservationTypeGeneration ObservationType = "GENERATION"
	ObservationTypeEvent      ObservationType = "EVENT"

	// Semantic taxonomy
	ObservationTypeLLM         ObservationType = "LLM"
	ObservationTypeEmbedding   ObservationType = "EMBEDDING"
	ObservationTypeTool        ObservationType = "TOOL"
	ObservationTypeAgent       ObservationType = "AGENT"
	ObservationTypeChain       ObservationType = "CHAIN"
	ObservationTypeRetriever   ObservationType = "RETRIEVER"
	ObservationTypeCache       ObservationType = "CACHE"
	ObservationTypeSandbox     ObservationType = "SANDBOX"
	ObservationTypeBrowser     ObservationType = "BROWSER"
	ObservationTypeComputer    ObservationType = "COMPUTER"
	ObservationTypeGuardrail   ObservationType = "GUARDRAIL"
	ObservationTypeScorer      ObservationType = "SCORER"
	ObservationTypeWorkflow    ObservationType = "WORKFLOW"
	ObservationTypeControl     ObservationType = "CONTROL"
	ObservationTypeHTTP        ObservationType = "HTTP"
	ObservationTypeIntegration ObservationType = "INTEGRATION"
	ObservationTypeHarness     ObservationType = "HARNESS"
	ObservationTypeMedia       ObservationType = "MEDIA"
)

// knownObservationTypes is the set used by NormalizeObservationType.
var knownObservationTypes = map[ObservationType]struct{}{
	ObservationTypeSpan: {}, ObservationTypeGeneration: {}, ObservationTypeEvent: {},
	ObservationTypeLLM: {}, ObservationTypeEmbedding: {}, ObservationTypeTool: {},
	ObservationTypeAgent: {}, ObservationTypeChain: {}, ObservationTypeRetriever: {},
	ObservationTypeCache: {}, ObservationTypeSandbox: {}, ObservationTypeBrowser: {},
	ObservationTypeComputer: {}, ObservationTypeGuardrail: {}, ObservationTypeScorer: {},
	ObservationTypeWorkflow: {}, ObservationTypeControl: {}, ObservationTypeHTTP: {},
	ObservationTypeIntegration: {}, ObservationTypeHarness: {}, ObservationTypeMedia: {},
}

// ObservationPurpose flags a span's role so it can be excluded from host rollups.
type ObservationPurpose string

const (
	// PurposeScorer marks scorer/facet executions that nest under a host trace
	// but must not count toward the host's cost/latency/token rollups (section 4.5).
	PurposeScorer ObservationPurpose = "scorer"
)

// ObservationOrigin records where a span came from (section 4.6).
type ObservationOrigin string

const (
	OriginProd       ObservationOrigin = "prod"
	OriginPlayground ObservationOrigin = "playground"
	OriginEval       ObservationOrigin = "eval"
	OriginReplay     ObservationOrigin = "replay"
	OriginSDK        ObservationOrigin = "sdk"
)

// RootType identifies an execution root for trace composition (section 4.7).
type RootType string

const (
	RootTypeAgent      RootType = "agent"
	RootTypeWorkflow   RootType = "workflow"
	RootTypePipeline   RootType = "pipeline"
	RootTypeHarness    RootType = "harness"
	RootTypeEval       RootType = "eval"
	RootTypePlayground RootType = "playground"
)

// NormalizeObservationType coerces an arbitrary type string to a known type,
// defaulting to SPAN. Matching is case-insensitive.
func NormalizeObservationType(s string) ObservationType {
	t := ObservationType(strings.ToUpper(strings.TrimSpace(s)))
	if _, ok := knownObservationTypes[t]; ok {
		return t
	}
	return ObservationTypeSpan
}

// WorkflowNodeTypeToObservationType maps a Studio workflow node type onto the
// semantic taxonomy (section 5.1) so a node and an equivalent agent step render
// identically. Unknown node types fall back to WORKFLOW.
func WorkflowNodeTypeToObservationType(nodeType string) ObservationType {
	switch strings.ToLower(strings.TrimSpace(nodeType)) {
	case "provider":
		return ObservationTypeGeneration
	case "cache":
		return ObservationTypeCache
	case "memory":
		return ObservationTypeRetriever
	case "agent":
		return ObservationTypeAgent
	case "function":
		return ObservationTypeTool
	case "inputguardrails", "outputguardrails":
		return ObservationTypeGuardrail
	case "tts", "stt", "voiceclone":
		return ObservationTypeMedia
	case "httprequest", "webhook":
		return ObservationTypeHTTP
	case "ifelse", "router", "loadbalancer", "auth", "ratelimiter":
		return ObservationTypeControl
	case "start", "response":
		return ObservationTypeWorkflow
	default:
		return ObservationTypeWorkflow
	}
}

// ObservationLevel represents the severity/importance level
type ObservationLevel string

const (
	ObservationLevelDebug   ObservationLevel = "DEBUG"
	ObservationLevelDefault ObservationLevel = "DEFAULT"
	ObservationLevelWarning ObservationLevel = "WARNING"
	ObservationLevelError   ObservationLevel = "ERROR"
)

// WithObservationType adds observation type to span options
func WithObservationType(obsType ObservationType) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.ObservationType, string(obsType)))
}

// WithObservationLevel adds observation level to span options
func WithObservationLevel(level ObservationLevel) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.ObservationLevel, string(level)))
}

// SetObservationType sets the observation type on an existing span
func SetObservationType(span trace.Span, obsType ObservationType) {
	span.SetAttributes(attribute.String(attrs.ObservationType, string(obsType)))
}

// SetObservationLevel sets the observation level on an existing span
func SetObservationLevel(span trace.Span, level ObservationLevel) {
	span.SetAttributes(attribute.String(attrs.ObservationLevel, string(level)))
}

// DetermineObservationTypeFromSpanName infers observation type from a span name.
// This is the backfill fallback for legacy and externally-ingested OTLP spans; new
// spans should set the type explicitly at emission (section 5). First matching
// prefix wins, so order from most specific to least.
func DetermineObservationTypeFromSpanName(spanName string) ObservationType {
	name := strings.ToLower(spanName)
	switch {
	case strings.HasPrefix(name, "agent.tool"):
		return ObservationTypeTool
	case strings.HasPrefix(name, "agent.session"), strings.HasPrefix(name, "agent.turn"), strings.HasPrefix(name, "agent."):
		return ObservationTypeAgent
	case strings.HasPrefix(name, "provider."):
		return ObservationTypeGeneration
	case strings.HasPrefix(name, "embedding."), strings.HasPrefix(name, "embeddings."):
		return ObservationTypeEmbedding
	case strings.HasPrefix(name, "vector."), strings.HasPrefix(name, "memory."), strings.HasPrefix(name, "retriever."):
		return ObservationTypeRetriever
	case strings.HasPrefix(name, "cache."):
		return ObservationTypeCache
	case strings.HasPrefix(name, "sandbox."):
		return ObservationTypeSandbox
	case strings.HasPrefix(name, "browser."):
		return ObservationTypeBrowser
	case strings.HasPrefix(name, "computer."):
		return ObservationTypeComputer
	case strings.HasPrefix(name, "guardrail."):
		return ObservationTypeGuardrail
	case strings.HasPrefix(name, "scorer."), strings.HasPrefix(name, "eval."), strings.HasPrefix(name, "facet."):
		return ObservationTypeScorer
	case strings.HasPrefix(name, "workflow."), strings.HasPrefix(name, "node."):
		return ObservationTypeWorkflow
	case strings.HasPrefix(name, "router."), strings.HasPrefix(name, "loadbalancer."), strings.HasPrefix(name, "ifelse."), strings.HasPrefix(name, "control."):
		return ObservationTypeControl
	case strings.HasPrefix(name, "http."), strings.HasPrefix(name, "httprequest."), strings.HasPrefix(name, "webhook."):
		return ObservationTypeHTTP
	case strings.HasPrefix(name, "integration."):
		return ObservationTypeIntegration
	case strings.HasPrefix(name, "harness."), strings.HasPrefix(name, "adk."):
		return ObservationTypeHarness
	case strings.HasPrefix(name, "a2a."):
		return ObservationTypeAgent
	case strings.HasPrefix(name, "mcp."):
		return ObservationTypeTool
	case strings.HasPrefix(name, "tts."), strings.HasPrefix(name, "stt."), strings.HasPrefix(name, "voice."):
		return ObservationTypeMedia
	case strings.HasPrefix(name, "stream."):
		return ObservationTypeEvent
	case strings.HasPrefix(name, "fallback."), strings.HasPrefix(name, "gateway."):
		return ObservationTypeSpan
	default:
		return ObservationTypeSpan
	}
}

// WithObservationPurpose marks a span's purpose (e.g. scorer) at start.
func WithObservationPurpose(p ObservationPurpose) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.ObservationPurpose, string(p)))
}

// WithObservationOrigin records a span's origin at start.
func WithObservationOrigin(o ObservationOrigin) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.ObservationOrigin, string(o)))
}

// WithRootType, WithRunID, and WithParentRunRef stamp the execution-root envelope
// used for trace composition across surfaces (section 4.7).
func WithRootType(rt RootType) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.RootType, string(rt)))
}

func WithRunID(id string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.RunID, id))
}

func WithParentRunRef(ref string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.ParentRunRef, ref))
}

// DetermineObservationLevel determines the level based on error status
func DetermineObservationLevel(hasError bool, spanName string) ObservationLevel {
	if hasError {
		return ObservationLevelError
	}

	// Debug level for low-level spans
	if len(spanName) > 7 && spanName[:7] == "stream." {
		return ObservationLevelDebug
	}

	return ObservationLevelDefault
}

// WithStepNumber adds step number to span options for execution ordering
func WithStepNumber(step uint32) trace.SpanStartOption {
	return trace.WithAttributes(attribute.Int(attrs.StepNumber, int(step)))
}

// WithNodeName adds workflow node name to span options
func WithNodeName(node string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(attrs.NodeName, node))
}

// SetStepNumber sets the step number on an existing span
func SetStepNumber(span trace.Span, step uint32) {
	span.SetAttributes(attribute.Int(attrs.StepNumber, int(step)))
}

// SetNodeName sets the node name on an existing span
func SetNodeName(span trace.Span, node string) {
	span.SetAttributes(attribute.String(attrs.NodeName, node))
}
