package service

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/email/types"
)

var uuidPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// The spend limit email names the scope in customer words, says "would be
// refused" for a blocking limit while the organization is not enforced,
// and carries no ids, tier codes or raw statuses.
func TestSpendLimitEmailVariants(t *testing.T) {
	resets := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		p           SpendLimitParams
		wantSubject string
		want        []string
		notWant     []string
	}{
		{"org 80 block shadow", SpendLimitParams{Period: "monthly", Percent: 80, Spent: 40, Limit: 50, Block: true},
			"The Acme organization is at 80% of its monthly spend limit",
			[]string{"80% of its <strong>$50.00</strong> monthly limit", "would be refused", "not enforced for this organization yet", "The whole organization", "$50.00 per month", "Block requests", "Oct 24, 2026 at 12:00 AM UTC", "manage billing for the Acme organization"},
			[]string{"are refused until", "Requests refused"}},
		{"org 100 block shadow", SpendLimitParams{Period: "monthly", Percent: 100, Spent: 51.5, Limit: 50, Block: true},
			"The Acme organization reached its monthly spend limit",
			[]string{"$51.50", "at or over its <strong>$50.00</strong> monthly limit", "would be refused now", "requests keep working"},
			[]string{"are refused until", "Requests refused"}},
		{"org 100 block enforced", SpendLimitParams{Period: "monthly", Percent: 100, Spent: 51.5, Limit: 50, Block: true, Enforced: true},
			"The Acme organization reached its monthly spend limit",
			[]string{"New requests from this organization are refused until the limit resets", "Requests refused"},
			[]string{"would be refused"}},
		{"key 80 alert only", SpendLimitParams{APIKeyName: "Production backend", Period: "daily", Percent: 80, Spent: 8, Limit: 10},
			`API key "Production backend" in the Acme organization is at 80% of its daily spend limit`,
			[]string{"API key &#34;Production backend&#34;", "only sends alerts", "Alert only", "today", "$10.00 per day"},
			[]string{"would be refused", "are refused"}},
		{"key 100 block creator", SpendLimitParams{APIKeyName: "Production backend", Period: "daily", Percent: 100, Spent: 10, Limit: 10, Block: true, Creator: true},
			`API key "Production backend" in the Acme organization reached its daily spend limit`,
			[]string{"Requests made with this API key would be refused now", `created API key "Production backend" in the Acme organization`},
			[]string{"manage billing"}},
	}
	for _, c := range cases {
		c.p.OrgName, c.p.OrgSlug, c.p.ResetsAt = "Acme", "acme", resets
		subject, html := renderBilling(t, types.EmailTypeSpendLimit, spendLimitData(c.p))
		assertCustomerCopy(t, types.EmailTypeSpendLimit, subject, html)
		if subject != c.wantSubject {
			t.Errorf("%s: subject = %q, want %q", c.name, subject, c.wantSubject)
		}
		for _, w := range c.want {
			if !strings.Contains(html, w) {
				t.Errorf("%s: missing %q", c.name, w)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(html, w) {
				t.Errorf("%s: unexpected %q", c.name, w)
			}
		}
		text := visibleText(html) + " " + subject
		if uuidPattern.MatchString(text) {
			t.Errorf("%s: an id reached the email: %s", c.name, uuidPattern.FindString(text))
		}
		for _, raw := range []string{"all_inference", "platform_credit", "notify", "estimated_cost", "basic", "free", "shadow", "enforce "} {
			if strings.Contains(strings.ToLower(text), raw) {
				t.Errorf("%s: internal code %q in customer copy", c.name, raw)
			}
		}
	}
}

// A hostile key name (set by an admin of the same organization) is escaped
// in the body and kept to one plain line in the subject.
func TestSpendLimitEmailEscapesTheKeyName(t *testing.T) {
	data := spendLimitData(SpendLimitParams{OrgName: "Acme", OrgSlug: "acme", APIKeyName: `<script>x</script> & "q"`,
		Period: "daily", Percent: 100, Spent: 10, Limit: 10, Block: true, ResetsAt: time.Now()})
	subject, html := renderBilling(t, types.EmailTypeSpendLimit, data)
	if strings.Contains(html, "<script>") {
		t.Fatal("key name rendered as HTML")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("key name missing from the body")
	}
	if strings.ContainsAny(subject, "\r\n") {
		t.Fatalf("subject is not one line: %q", subject)
	}
}

// A crossing seen just after the window reset is told in the past tense and
// promises nothing about refusals in a window that is over.
func TestSpendLimitEmailForAnEndedWindow(t *testing.T) {
	resets := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	data := spendLimitData(SpendLimitParams{OrgName: "Acme", OrgSlug: "acme", Period: "daily", Percent: 100, Spent: 10, Limit: 10,
		Block: true, ResetsAt: resets, CheckedAt: resets.Add(4 * time.Minute)})
	subject, html := renderBilling(t, types.EmailTypeSpendLimit, data)
	assertCustomerCopy(t, types.EmailTypeSpendLimit, subject, html)
	for _, w := range []string{"yesterday", "window ended on Oct 24, 2026 at 12:00 AM UTC", "counts from zero again"} {
		if !strings.Contains(html, w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, w := range []string{"would be refused", "are refused", "today"} {
		if strings.Contains(html, w) {
			t.Errorf("unexpected %q", w)
		}
	}
}
