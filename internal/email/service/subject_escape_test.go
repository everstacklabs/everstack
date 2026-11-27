package service

import (
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/templates"
	"github.com/everstacklabs/everstack/internal/email/types"
)

// Subjects are plain text: an apostrophe, ampersand or angle bracket in an
// organization or inviter name must reach the inbox as typed. Bodies stay
// HTML-escaped.
func TestSubjectsArePlainTextAndBodiesStayEscaped(t *testing.T) {
	const org, inviter = `O'Brien & Co <Labs>`, `Seán O'Neil & "Partners"`
	cases := []struct {
		typ     types.EmailType
		data    map[string]interface{}
		subject string
	}{
		{types.EmailTypeInvitation, invitationData("new@example.com", org, inviter, "sean@obrien.com", "https://app.everstack.ai/invite/t", "member", time.Time{}),
			`Seán O'Neil & "Partners" invited you to the O'Brien & Co <Labs> organization on Everstack`},
		{types.EmailTypeSubscriptionStarted, subscriptionStartedData(SubscriptionStartedParams{OrgName: org, OrgSlug: "obrien", PlanName: "Build"}),
			`The O'Brien & Co <Labs> organization is on the Everstack Build plan`},
		{types.EmailTypeCreditLowBalance, lowBalanceData(LowBalanceParams{OrgName: org, OrgSlug: "obrien", Depleted: true, Funded: 10}),
			`The O'Brien & Co <Labs> organization is out of Everstack credit`},
	}
	for _, c := range cases {
		subject, html := renderBilling(t, c.typ, c.data)
		if subject != c.subject {
			t.Errorf("%s: subject = %q, want %q", c.typ, subject, c.subject)
		}
		if strings.Contains(html, "<Labs>") || !strings.Contains(html, "&lt;Labs&gt;") {
			t.Errorf("%s: body does not escape the organization name", c.typ)
		}
	}
}

func TestSubjectIsOneLine(t *testing.T) {
	subject, _ := renderBilling(t, types.EmailTypeSubscriptionStarted, subscriptionStartedData(SubscriptionStartedParams{OrgName: "Acme\r\nBcc: x@evil.test", OrgSlug: "acme", PlanName: "Build"}))
	if strings.ContainsAny(subject, "\r\n") {
		t.Fatalf("subject spans lines: %q", subject)
	}
}

// The invitation's From and Reply-To are ours, whatever the inviter calls
// themselves: a name like "PayPal Billing" must not become the sender an
// inbox shows, and replies must not go to an address the inviter picked.
func TestInvitationHeadersCarryNoInviterInput(t *testing.T) {
	s := &EmailService{config: config.DefaultConfig()}
	req := s.invitationRequest("new@example.com", "Acme", "PayPal Billing <billing@paypal.com>", "attacker@evil.test",
		"https://app.everstack.ai/invite/t", "member", time.Time{})

	if req.FromOverride != "Everstack <invites@everstack.ai>" {
		t.Errorf("From = %q, want the fixed Everstack sender", req.FromOverride)
	}
	if req.ReplyToOverride != "" {
		t.Errorf("Reply-To override = %q, want the sender's own", req.ReplyToOverride)
	}
	// The name still appears, escaped, in the body.
	_, html := renderAccount(t, types.EmailTypeInvitation, req.Data)
	if !strings.Contains(html, "PayPal Billing &lt;billing@paypal.com&gt;") {
		t.Error("the inviter's name should appear escaped in the body")
	}
}

// Subjects carry organization, inviter and user names. Whatever those hold,
// the header is one line of printable text of bounded length.
func TestSubjectDropsControlCharactersAndIsCapped(t *testing.T) {
	subject, _ := renderAccount(t, types.EmailTypeInvitation, invitationData("new@example.com",
		"Ac\x00me\x1b[31m", "Ana\u202Egnp.exe\u2066", "a@b.co", "https://app.everstack.ai/invite/t", "", time.Time{}))
	for _, r := range subject {
		if r < 0x20 || r == 0x7f || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			t.Fatalf("subject keeps control character %U: %q", r, subject)
		}
	}
	if !strings.Contains(subject, "Anagnp.exe invited you to the Acme[31m organization") {
		t.Errorf("subject = %q", subject)
	}

	long, _ := renderBilling(t, types.EmailTypeSubscriptionStarted, subscriptionStartedData(SubscriptionStartedParams{
		OrgName: strings.Repeat("Big Org ", 100), OrgSlug: "big", PlanName: "Build"}))
	if n := len([]rune(long)); n > templates.MaxSubjectLength {
		t.Errorf("subject is %d characters, want at most %d", n, templates.MaxSubjectLength)
	}
	if !strings.HasSuffix(long, "...") {
		t.Errorf("a cut subject should say so: %q", long)
	}
}
