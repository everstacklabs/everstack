package attributes

import (
	"encoding/json"
)

// SpanType identifies different types of spans for truncation configuration
type SpanType string

const (
	SpanTypeNormalization SpanType = "normalization"
	SpanTypeResolution    SpanType = "resolution"
	SpanTypeProvider      SpanType = "provider"
	SpanTypeResponse      SpanType = "response"
	SpanTypeDefault       SpanType = "default"
)

// TruncationLimits defines byte limits for JSON serialization per span type
// Different span types need different amounts of detail:
// - Normalization needs full details for debugging request issues
// - Provider calls need moderate detail
// - Response processing needs to capture full responses
var TruncationLimits = map[SpanType]int{
	SpanTypeNormalization: 50 * 1024, // 50KB - needs full request details
	SpanTypeResolution:    10 * 1024, // 10KB - moderate detail
	SpanTypeProvider:      10 * 1024, // 10KB - moderate detail
	SpanTypeResponse:      20 * 1024, // 20KB - needs full response details
	SpanTypeDefault:       10 * 1024, // 10KB - default fallback
}

// GetTruncationLimit returns the truncation limit for a given span type
func GetTruncationLimit(spanType SpanType) int {
	if limit, ok := TruncationLimits[spanType]; ok {
		return limit
	}
	return TruncationLimits[SpanTypeDefault]
}

// SerializeAndTruncate serializes an object to JSON and truncates if needed
func SerializeAndTruncate(v interface{}, spanType SpanType) string {
	if v == nil {
		return ""
	}

	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	maxBytes := GetTruncationLimit(spanType)
	return TruncateString(string(data), maxBytes)
}

// TruncateString truncates a string to maxLen with an indicator if truncated
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	const suffix = "...[truncated]"
	if maxLen <= len(suffix) {
		return suffix[:maxLen]
	}

	return s[:maxLen-len(suffix)] + suffix
}

// SerializeToJSON serializes an object to JSON string, handling errors gracefully
// This function does NOT truncate - use for storing complete payloads in traces
func SerializeToJSON(v interface{}) string {
	if v == nil {
		return ""
	}

	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	return string(data)
}

// SerializeToJSONPretty serializes an object to pretty-printed JSON string
// This function does NOT truncate - use for storing complete payloads in traces
func SerializeToJSONPretty(v interface{}) string {
	if v == nil {
		return ""
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}

	return string(data)
}
