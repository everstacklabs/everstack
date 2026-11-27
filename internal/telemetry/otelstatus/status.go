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
	unsetSpellings = []string{"unset", "status_code_unset"}
)

// IsErrorValue reports whether a status value read back from ClickHouse is an
// error, under either spelling.
//
// The SQL builders above cover comparisons made *inside* a query. This covers
// the other half: a status that has already been scanned into Go. Comparing it
// with a hand-written `strings.EqualFold(status, "error")` is the same bug in a
// different place -- it matches "Error" and silently misses "STATUS_CODE_ERROR".
//
// Do not "simplify" this to whichever spelling looks dominant where you happen
// to be looking: the two environments invert, because dev is dominated by the
// OTLP HTTP ingest path and prod by the collector exporter. Measured
// 2026-09-08/09, short spelling vs enum:
//
//	dev    64 short / 268 enum    -- matching only "error" loses 268 of 332
//	prod   34 short /   7 enum    -- matching only the enum loses 34 of 41
//
// Whichever environment you sample, the other one's majority spelling is the
// one you would throw away. Both must match.
func IsErrorValue(status string) bool {
	lowered := strings.ToLower(strings.TrimSpace(status))
	for _, spelling := range errorSpellings {
		if lowered == spelling {
			return true
		}
	}
	return false
}

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

// IsUnset renders a predicate matching spans that carry no explicit status.
// This is the overwhelming majority of stored spans, so it exists mainly so a
// caller-supplied "UNSET" filter can be honoured rather than silently matching
// nothing.
func IsUnset(column string) string {
	return membership(column, "IN", unsetSpellings)
}

// IsNotUnset renders the negation of IsUnset: spans explicitly marked either
// successful or failed.
func IsNotUnset(column string) string {
	return membership(column, "NOT IN", unsetSpellings)
}

// The canonical short names this package hands back from Normalize. They are
// the vocabulary the trace APIs have always exposed to callers, so they are
// what an API validator should echo in an error message.
const (
	CanonicalOK    = "OK"
	CanonicalError = "ERROR"
	CanonicalUnset = "UNSET"
)

// Normalize maps any spelling a caller might send for a span status onto one of
// the canonical short names, reporting whether it was recognised at all.
//
// It deliberately accepts more than the API has historically documented: the
// uppercase short names callers already send ("OK"/"ERROR"/"UNSET"), the mixed
// case spellings actually stored by the collector exporter ("Ok"/"Error"/
// "Unset"), and the protobuf enum names written by the Everstack OTLP HTTP
// handler. Accepting a spelling here never widens what a query can reach --
// every one of them compiles to the same membership predicate.
func Normalize(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ok", "status_code_ok":
		return CanonicalOK, true
	case "error", "status_code_error":
		return CanonicalError, true
	case "unset", "status_code_unset":
		return CanonicalUnset, true
	default:
		return "", false
	}
}

// Equals renders an equality predicate for a caller-supplied status value.
//
// Binding the caller's value straight into `StatusCode = ?` is the shape of
// this bug: the value a caller is allowed to send is not the value the column
// stores, so the filter returns an empty result set instead of an error. Route
// caller-facing status filters through here so the two vocabularies stay
// connected.
func Equals(column, value string) (string, error) {
	canonical, ok := Normalize(value)
	if !ok {
		return "", fmt.Errorf("unknown OTel status %q: want one of %s, %s, %s", value, CanonicalOK, CanonicalError, CanonicalUnset)
	}
	switch canonical {
	case CanonicalOK:
		return IsOK(column), nil
	case CanonicalError:
		return IsError(column), nil
	default:
		return IsUnset(column), nil
	}
}

func membership(column, op string, spellings []string) string {
	quoted := make([]string, 0, len(spellings))
	for _, s := range spellings {
		quoted = append(quoted, "'"+s+"'")
	}
	return fmt.Sprintf("lower(%s) %s (%s)", column, op, strings.Join(quoted, ", "))
}
