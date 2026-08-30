// Package redact removes PII from free text before it is stored (D7: redaction
// before storage; ties to the non-negotiable no-tenant-data-leak rule). It is
// distinct from the credential/auth-header redactors elsewhere: this redacts
// content patterns (SSN, email, credit card) that may ride in trace input/output
// payloads. Pure and regex-based so it can run in the ingestion path or an
// OTel-Collector processor.
package redact

import (
	"regexp"
	"sort"
)

// Kind identifies a class of redacted PII.
type Kind string

const (
	KindEmail      Kind = "email"
	KindSSN        Kind = "ssn"
	KindCreditCard Kind = "credit_card"
)

var (
	emailRe     = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	ssnRe       = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	ccGroupedRe = regexp.MustCompile(`\b(?:\d{4}[ -]){3}\d{4}\b`)
	ccPlainRe   = regexp.MustCompile(`\b\d{13,19}\b`)
)

// Result is the redacted text plus the kinds of PII that were found.
type Result struct {
	Text  string
	Found []Kind
}

// Redact replaces SSN, email, and credit-card patterns in s with typed
// placeholders and reports which kinds were found (deduped, sorted). Email is
// redacted first so digits inside an address are not re-matched as a card.
func Redact(s string) Result {
	found := map[Kind]bool{}
	apply := func(re *regexp.Regexp, kind Kind, placeholder string) {
		if re.MatchString(s) {
			found[kind] = true
			s = re.ReplaceAllString(s, placeholder)
		}
	}
	apply(emailRe, KindEmail, "[REDACTED_EMAIL]")
	apply(ssnRe, KindSSN, "[REDACTED_SSN]")
	apply(ccGroupedRe, KindCreditCard, "[REDACTED_CC]")
	apply(ccPlainRe, KindCreditCard, "[REDACTED_CC]")

	kinds := make([]Kind, 0, len(found))
	for k := range found {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return Result{Text: s, Found: kinds}
}

// HasPII reports whether s contains any recognized PII pattern.
func HasPII(s string) bool {
	return emailRe.MatchString(s) || ssnRe.MatchString(s) ||
		ccGroupedRe.MatchString(s) || ccPlainRe.MatchString(s)
}
