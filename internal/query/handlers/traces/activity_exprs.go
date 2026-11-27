package traces

// Read-time semantic-convention fragments for callers outside this package
// that aggregate model calls from otel_traces (the control plane's org
// activity summary). They are the same fragments the trace views use, so a
// model or token count means the same thing on every surface. Every fragment
// inlines its attribute names and binds nothing.

// ModelExprSQL is a span's model name, coalesced across conventions.
func ModelExprSQL() string { return modelSQL() }

// TotalTokensExprSQL is a span's total tokens: the explicit total when one
// is reported, otherwise input + output + cache.
func TotalTokensExprSQL() string { return totalTokensSQL() }
