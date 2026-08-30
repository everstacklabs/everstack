package traces

import (
	"encoding/json"
	"time"
)

// ClickHouseTrace represents a trace record from ClickHouse
type ClickHouseTrace struct {
	Timestamp          time.Time
	TraceID            string
	SpanID             string
	ParentSpanID       string
	SpanName           string
	SpanKind           string
	ServiceName        string
	Duration           int64 // nanoseconds (int64 to match ClickHouse Int64)
	StatusCode         string
	StatusMessage      string
	SpanAttributes     map[string]string
	ResourceAttributes map[string]string
}

// TransformToEverstack converts ClickHouse traces to Everstack format
func TransformTrace(traces []ClickHouseTrace, scores map[string][]EverstackScore) []EverstackTrace {
	// Group traces by TraceID
	traceGroups := make(map[string][]ClickHouseTrace)
	for _, t := range traces {
		traceGroups[t.TraceID] = append(traceGroups[t.TraceID], t)
	}

	result := make([]EverstackTrace, 0, len(traceGroups))

	for traceID, spans := range traceGroups {
		EverstackTrace := buildEverstackTrace(traceID, spans, scores[traceID])
		result = append(result, EverstackTrace)
	}

	return result
}

// buildEverstackTrace creates a Everstack trace from a group of spans
func buildEverstackTrace(traceID string, spans []ClickHouseTrace, traceScores []EverstackScore) EverstackTrace {
	if len(spans) == 0 {
		return EverstackTrace{ID: traceID}
	}

	// Find root span (no parent)
	var rootSpan *ClickHouseTrace
	for i := range spans {
		if spans[i].ParentSpanID == "" {
			rootSpan = &spans[i]
			break
		}
	}

	if rootSpan == nil {
		// No root span found, use first span
		rootSpan = &spans[0]
	}

	// Build trace-level information from root span
	trace := EverstackTrace{
		ID:           traceID,
		Timestamp:    rootSpan.Timestamp,
		HTMLPath:     "/traces/" + traceID, // Default path
		Observations: make([]EverstackObservation, 0, len(spans)),
		Scores:       traceScores,
	}

	// Extract trace-level attributes from root span
	if name := getStringAttr(rootSpan.SpanAttributes, "trace.name"); name != "" {
		trace.Name = &name
	}

	// Coalesce user/session across semconvs so OTel-GenAI and OpenInference spans
	// group into sessions/users the same as native ones (A1).
	if userID := UserFromAttrs(rootSpan.SpanAttributes); userID != "" {
		trace.UserID = &userID
	}

	if sessionID := SessionFromAttrs(rootSpan.SpanAttributes); sessionID != "" {
		trace.SessionID = &sessionID
	}

	if env := getStringAttr(rootSpan.ResourceAttributes, "deployment.environment"); env != "" {
		trace.Environment = &env
	}

	if release := getStringAttr(rootSpan.ResourceAttributes, "service.version"); release != "" {
		trace.Release = &release
	}

	// Parse input/output if present (multi-semconv: Everstack `trace.input`,
	// OpenInference `input.value`).
	if inputStr := AttrFromMap(rootSpan.SpanAttributes, inputAttrs...); inputStr != "" {
		var input interface{}
		if err := json.Unmarshal([]byte(inputStr), &input); err == nil {
			trace.Input = input
		}
	}

	if outputStr := AttrFromMap(rootSpan.SpanAttributes, outputAttrs...); outputStr != "" {
		var output interface{}
		if err := json.Unmarshal([]byte(outputStr), &output); err == nil {
			trace.Output = output
		}
	}

	// Parse metadata if present
	if metadataStr := getStringAttr(rootSpan.SpanAttributes, "trace.metadata"); metadataStr != "" {
		var metadata interface{}
		if err := json.Unmarshal([]byte(metadataStr), &metadata); err == nil {
			trace.Metadata = metadata
		}
	}

	// Parse tags if present
	if tagsStr := getStringAttr(rootSpan.SpanAttributes, "trace.tags"); tagsStr != "" {
		var tags []string
		if err := json.Unmarshal([]byte(tagsStr), &tags); err == nil {
			trace.Tags = tags
		}
	}

	// Calculate total latency and cost
	var totalDuration int64
	var totalCost float64

	for i := range spans {
		obs := buildEverstackObservation(&spans[i])
		trace.Observations = append(trace.Observations, obs)

		if spans[i].Duration > totalDuration {
			totalDuration = spans[i].Duration
		}

		if obs.CalculatedTotalCost != nil {
			totalCost += *obs.CalculatedTotalCost
		}
	}

	trace.Latency = float64(totalDuration) / 1e9 // Convert nanoseconds to seconds
	trace.TotalCost = totalCost

	return trace
}

// buildEverstackObservation creates a Everstack observation from a span
func buildEverstackObservation(span *ClickHouseTrace) EverstackObservation {
	obs := EverstackObservation{
		ID:        span.SpanID,
		TraceID:   &span.TraceID,
		StartTime: span.Timestamp,
	}

	// Set name
	obs.Name = &span.SpanName

	// Set observation type from attributes
	obsType := getStringAttr(span.SpanAttributes, "observation.type")
	if obsType == "" {
		obsType = "SPAN" // Default
	}
	obs.Type = obsType

	// Set observation level
	obsLevel := getStringAttr(span.SpanAttributes, "observation.level")
	if obsLevel == "" {
		obsLevel = "DEFAULT"
	}
	obs.Level = obsLevel

	// Calculate end time
	endTime := span.Timestamp.Add(time.Duration(span.Duration))
	obs.EndTime = &endTime

	// Calculate latency in seconds
	latency := float64(span.Duration) / 1e9
	obs.Latency = &latency

	// Set parent if exists
	if span.ParentSpanID != "" {
		obs.ParentObservationID = &span.ParentSpanID
	}

	// Extract model information (multi-semconv: Everstack `llm.model`, GenAI
	// `gen_ai.{response,request}.model`, OpenInference `llm.model_name`).
	if model := ModelFromAttrs(span.SpanAttributes); model != "" {
		obs.Model = &model
		obs.ModelID = &model
	}

	// Extract status message
	if span.StatusMessage != "" {
		obs.StatusMessage = &span.StatusMessage
	}

	// Parse model parameters
	if paramsStr := getStringAttr(span.SpanAttributes, "llm.request.model_parameters"); paramsStr != "" {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(paramsStr), &params); err == nil {
			obs.ModelParameters = params
		}
	}

	// Parse input (request messages) across semconvs so OTel-GenAI / OpenInference
	// generation spans show their messages too.
	if inputStr := SpanInputFromAttrs(span.SpanAttributes); inputStr != "" {
		var input interface{}
		if err := json.Unmarshal([]byte(inputStr), &input); err == nil {
			obs.Input = input
		}
	}

	// Parse output (response choices) across semconvs.
	if outputStr := SpanOutputFromAttrs(span.SpanAttributes); outputStr != "" {
		var output interface{}
		if err := json.Unmarshal([]byte(outputStr), &output); err == nil {
			obs.Output = output
		}
	}

	// Parse usage details
	if usageStr := getStringAttr(span.SpanAttributes, "llm.usage_details"); usageStr != "" {
		var usage map[string]int64
		if err := json.Unmarshal([]byte(usageStr), &usage); err == nil {
			obs.UsageDetails = usage
		}
	}

	// Parse cost details
	if costStr := getStringAttr(span.SpanAttributes, "llm.cost_details"); costStr != "" {
		var costs map[string]float64
		if err := json.Unmarshal([]byte(costStr), &costs); err == nil {
			obs.CostDetails = costs

			// Also populate calculated cost fields
			if inputCost, ok := costs["input"]; ok {
				obs.CalculatedInputCost = &inputCost
			}
			if outputCost, ok := costs["output"]; ok {
				obs.CalculatedOutputCost = &outputCost
			}
			if totalCost, ok := costs["total"]; ok {
				obs.CalculatedTotalCost = &totalCost
			}
		}
	}

	// Create deprecated Usage object for backward compatibility
	if obs.UsageDetails != nil {
		usage := &EverstackUsage{}

		if input, ok := obs.UsageDetails["input"]; ok {
			usage.Input = &input
		}
		if output, ok := obs.UsageDetails["output"]; ok {
			usage.Output = &output
		}
		if total, ok := obs.UsageDetails["total"]; ok {
			usage.Total = &total
		}

		unit := "TOKENS"
		usage.Unit = &unit

		if obs.CalculatedInputCost != nil {
			usage.InputCost = obs.CalculatedInputCost
		}
		if obs.CalculatedOutputCost != nil {
			usage.OutputCost = obs.CalculatedOutputCost
		}
		if obs.CalculatedTotalCost != nil {
			usage.TotalCost = obs.CalculatedTotalCost
		}

		obs.Usage = usage
	}

	// Extract environment
	if env := getStringAttr(span.ResourceAttributes, "deployment.environment"); env != "" {
		obs.Environment = &env
	}

	return obs
}

// getStringAttr safely gets a string attribute from a map
func getStringAttr(attrs map[string]string, key string) string {
	if val, ok := attrs[key]; ok {
		return val
	}
	return ""
}
