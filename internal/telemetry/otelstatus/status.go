// Package otelstatus centralises how a span's OpenTelemetry status is compared
// in ClickHouse SQL.
//
// otel_traces stores that status under two different spellings depending on
// which ingest path wrote the span. The OpenTelemetry collector exporter
// writes the short form -- "Ok" / "Error" / "Unset" -- and that is what every
// gateway-produced span carries today. The Everstack OTLP HTTP handler
// (internal/api/http/otlp/handler.go) instead writes the protobuf enum name,
// "STATUS_CODE_OK" / "STATUS_CODE_ERROR" / "STATUS_CODE_UNSET". Both are
// legitimately present, so a reader must accept either.
//
// ClickHouse "=" is case-sensitive, so a query that hard-codes one spelling
// silently reports zero of the other. That is exactly how the public model
// catalog came to report a success rate of 1.0 for every model: its
// materialized view counted errors with StatusCode = 'STATUS_CODE_ERROR',
// which no provider span has ever matched. Route every status comparison
// through this package so a future exporter change cannot re-break it one
// query at a time.
package otelstatus

import (
	"fmt"
	"strings"
)

// Column is the otel_traces column holding the span status. Callers that
// query a joined or aliased table pass their own qualified name instead.
const Column = "StatusCode"

// errorSpellings and okSpellings are lower-cased because every predicate this
// package builds lower-cases the column before comparing.
var (
	errorSpellings = []string{"error", "status_code_error"}
	okSpellings    = []string{"ok", "status_code_ok"}
)

// IsError renders a predicate matching spans whose status is an error, under
// either spelling.
func IsError(column string) string {
	return membership(column, "IN", errorSpellings)
}

// IsNotError renders the negation of IsError. An unset status is NOT an error,
// so this matches "Ok", "Unset" and their enum-name equivalents alike.
func IsNotError(column string) string {
	return membership(column, "NOT IN", errorSpellings)
}

// IsOK renders a predicate matching spans explicitly marked successful. This
// is deliberately narrower than IsNotError: it excludes an unset status.
func IsOK(column string) string {
	return membership(column, "IN", okSpellings)
}

// IsNotOK renders the negation of IsOK: everything that is not an explicit
// success, which includes both errors and an unset status.
func IsNotOK(column string) string {
	return membership(column, "NOT IN", okSpellings)
}

func membership(column, op string, spellings []string) string {
	quoted := make([]string, 0, len(spellings))
	for _, s := range spellings {
		quoted = append(quoted, "'"+s+"'")
	}
	return fmt.Sprintf("lower(%s) %s (%s)", column, op, strings.Join(quoted, ", "))
}
