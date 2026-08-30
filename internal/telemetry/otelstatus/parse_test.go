package otelstatus

import (
	"strings"
	"testing"
)

func lower(s string) string { return strings.ToLower(s) }

// parseMembership pulls the spelling set and the negation flag back out of a
// rendered predicate so the tests can evaluate it without a database.
func parseMembership(t *testing.T, clause string) (map[string]struct{}, bool) {
	t.Helper()
	negated := strings.Contains(clause, " NOT IN (")
	open := strings.Index(clause, "(")
	open = strings.Index(clause[open+1:], "(") + open + 1
	closeIdx := strings.LastIndex(clause, ")")
	if open < 0 || closeIdx <= open {
		t.Fatalf("could not parse membership clause %q", clause)
	}
	set := make(map[string]struct{})
	for _, raw := range strings.Split(clause[open+1:closeIdx], ",") {
		set[strings.Trim(strings.TrimSpace(raw), "'")] = struct{}{}
	}
	return set, negated
}
