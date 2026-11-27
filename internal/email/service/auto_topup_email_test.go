package service

import (
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/email/types"
)

func TestAutoTopUpChargedEmail(t *testing.T) {
	at := time.Date(2026, 10, 6, 14, 30, 0, 0, time.UTC)
	data := autoTopUpData(AutoTopUpParams{
		OrgName: "Acme", OrgSlug: "acme", Outcome: AutoTopUpCharged, Amount: 25, CardLabel: "Visa ending in 4242",
		Balance: 31.5, BalanceKnown: true, RemainingCap: 50, MonthlyCap: 75, RemainingCapKnown: true, At: at,
	})
	subject, html := renderBilling(t, types.EmailTypeAutoTopUpCharged, data)
	assertCustomerCopy(t, types.EmailTypeAutoTopUpCharged, subject, html)
	if subject != "Auto top-up charged $25.00 for the Acme organization" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{"Visa ending in 4242", "$31.50", "$50.00", "$75.00", "Left this month", "turn auto top-up off"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Unknown balance and cap hide their rows.
	data = autoTopUpData(AutoTopUpParams{OrgName: "Acme", OrgSlug: "acme", Outcome: AutoTopUpCharged, Amount: 25})
	_, html = renderBilling(t, types.EmailTypeAutoTopUpCharged, data)
	if strings.Contains(html, "New balance") || strings.Contains(html, "Left this month") || strings.Contains(html, "Charged to") {
		t.Fatal("optional rows must hide when their value is unknown")
	}
	if !strings.Contains(html, "your default card") {
		t.Fatal("body must fall back to 'your default card' without a card label")
	}
}

func TestAutoTopUpFailedEmailVariants(t *testing.T) {
	at := time.Date(2026, 10, 6, 14, 30, 0, 0, time.UTC)
	reason := "Your card was declined. Update the card or set a different default card."
	cases := []struct {
		name        string
		p           AutoTopUpParams
		wantSubject string
		want        []string
	}{
		{"retrying", AutoTopUpParams{Amount: 25, CardLabel: "Visa ending in 4242", Reason: reason, NextAttempt: at.Add(time.Hour), At: at},
			"Auto top-up failed, action needed for the Acme organization",
			[]string{reason, "We will try again after", "Next try", "Fix in billing"}},
		{"paused", AutoTopUpParams{Amount: 25, CardLabel: "Visa ending in 4242", Reason: reason, Paused: true, At: at},
			"Auto top-up paused, action needed for the Acme organization",
			[]string{"auto top-up is paused", "save your auto top-up settings"}},
	}
	for _, c := range cases {
		c.p.OrgName, c.p.OrgSlug, c.p.Outcome = "Acme", "acme", AutoTopUpFailed
		subject, html := renderBilling(t, types.EmailTypeAutoTopUpFailed, autoTopUpData(c.p))
		assertCustomerCopy(t, types.EmailTypeAutoTopUpFailed, subject, html)
		if subject != c.wantSubject {
			t.Errorf("%s: subject = %q, want %q", c.name, subject, c.wantSubject)
		}
		for _, w := range c.want {
			if !strings.Contains(html, w) {
				t.Errorf("%s: missing %q", c.name, w)
			}
		}
		if c.p.Paused && strings.Contains(html, "Next try") {
			t.Errorf("%s: a paused notice must not promise a next try", c.name)
		}
	}
}

func TestAutoTopUpPausedForDisputeEmail(t *testing.T) {
	data := autoTopUpData(AutoTopUpParams{
		OrgName: "Acme", OrgSlug: "acme", Outcome: AutoTopUpFailed, Paused: true, PauseReason: "dispute",
		Reason: "A credit payment was disputed with your bank, so we will not charge your card again until an owner or admin turns auto top-up back on.",
		At:     time.Date(2026, 10, 6, 14, 30, 0, 0, time.UTC),
	})
	subject, html := renderBilling(t, types.EmailTypeAutoTopUpFailed, data)
	assertCustomerCopy(t, types.EmailTypeAutoTopUpFailed, subject, html)
	if subject != "Auto top-up paused, action needed for the Acme organization" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{"disputed with your bank", "turn auto top-up back on", ">Paused<"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, not := range []string{"did not go through", "$0.00", "Next try"} {
		if strings.Contains(html, not) {
			t.Errorf("dispute pause notice says %q", not)
		}
	}
}
