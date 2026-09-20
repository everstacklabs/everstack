package otelstatus

import "testing"

// Both spellings are legitimately present in otel_traces. A hand-written
// strings.EqualFold(status, "error") matches only the short form and silently
// misses the enum name, which on 2026-09-08 was 79 of 90 error spans.
func TestIsErrorValueAcceptsBothSpellings(t *testing.T) {
	for _, status := range []string{
		"Error", "error", "ERROR",
		"STATUS_CODE_ERROR", "status_code_error", "Status_Code_Error",
		"  Error  ", // scanned values can carry padding
	} {
		if !IsErrorValue(status) {
			t.Errorf("IsErrorValue(%q) = false, want true", status)
		}
	}
}

// Unset must not count as an error: most spans are Unset, and treating them
// as failures would invert the bug rather than fix it.
func TestIsErrorValueRejectsNonErrors(t *testing.T) {
	for _, status := range []string{
		"Ok", "ok", "STATUS_CODE_OK",
		"Unset", "STATUS_CODE_UNSET",
		"", "   ", "unknown",
	} {
		if IsErrorValue(status) {
			t.Errorf("IsErrorValue(%q) = true, want false", status)
		}
	}
}
