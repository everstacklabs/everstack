package service

import (
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/types"
)

func renderAccount(t *testing.T, typ types.EmailType, data map[string]interface{}) (string, string) {
	t.Helper()
	return renderBilling(t, typ, data)
}

func TestAuthEmailsNameTheAccountTimeAndExpiry(t *testing.T) {
	const to = "ada@example.com"
	cases := []struct {
		typ      types.EmailType
		data     map[string]interface{}
		subject  string
		link     string
		wants    []string
		security string
	}{
		{types.EmailTypeMagicLink, magicLinkData(to, "https://app.everstack.ai/verify?token=t1", "24 hours", noticeAt),
			"Your Everstack sign-in link", "https://app.everstack.ai/verify?token=t1",
			[]string{"Sign in to Everstack", "Link expires In 24 hours", "sign in to Everstack as ada@example.com. The link works once."},
			"If you didn't ask to sign in, ignore this email. Nobody can sign in without this link, and it stops working after 24 hours."},
		{types.EmailTypePasswordReset, passwordResetData(to, "https://app.everstack.ai/reset-password?token=t2", "1 hour", noticeAt),
			"Reset your Everstack password", "https://app.everstack.ai/reset-password?token=t2",
			[]string{"Reset your password", "Link expires In 1 hour", "new password for the Everstack account ada@example.com."},
			"If you didn't ask to reset your password, ignore this email. Your password stays the same, and the link stops working after 1 hour."},
		{types.EmailTypeEmailVerification, verificationData(to, "482913", "10 minutes", noticeAt),
			"Your Everstack verification code", "",
			[]string{"Verify your email", "Code expires In 10 minutes", "Enter this code on Everstack to confirm ada@example.com.", "482913"},
			"If you didn't ask for this code, ignore this email."},
	}
	for _, c := range cases {
		subject, html := renderAccount(t, c.typ, c.data)
		assertClean(t, subject, html)
		if subject != c.subject {
			t.Errorf("%s: subject = %q", c.typ, subject)
		}
		assertText(t, string(c.typ), html, append(c.wants, "Account ada@example.com", "Requested Oct 4, 2026 at 6:32 PM UTC", c.security, "support@everstack.ai")...)
		if c.link != "" && strings.Count(html, c.link) != 2 {
			t.Errorf("%s: link should appear in the button and the fallback once each", c.typ)
		}
		if strings.Contains(html, "billing") {
			t.Errorf("%s: account email mentions billing", c.typ)
		}
		if got, want := config.DefaultConfig().SenderFor(c.typ), config.DefaultConfig().Senders.Accounts; got != want {
			t.Errorf("%s sent from %+v", c.typ, got)
		}
	}
}

func TestAuthDataUsesTheClock(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Minute)
	data := magicLinkData("a@example.com", "https://x", "10 minutes", time.Now())
	at, err := time.Parse("Jan 2, 2006 at 3:04 PM UTC", data["requested_at"].(string))
	if err != nil || at.Before(before) {
		t.Fatalf("requested_at = %v (%v)", data["requested_at"], err)
	}
}

func TestInvitationNamesInviterOrganizationRoleAndExpiry(t *testing.T) {
	expires := time.Date(2026, 10, 11, 18, 32, 0, 0, time.UTC)
	subject, html := renderAccount(t, types.EmailTypeInvitation,
		invitationData("new@example.com", "Acme", "Jane Smith", "jane@acme.com", "https://app.everstack.ai/invite/tok", "admin", expires))
	assertClean(t, subject, html)
	if subject != "Jane Smith invited you to the Acme organization on Everstack" {
		t.Errorf("subject = %q", subject)
	}
	assertText(t, "invitation", html, "Join the Acme organization on Everstack", "Organization Acme", "Invited by Jane Smith (jane@acme.com)",
		"Role Admin", "Invitation for new@example.com", "Expires Oct 11, 2026 at 6:32 PM UTC",
		"Jane Smith invited you to join the Acme organization on Everstack with the Admin role.",
		"expires on Oct 11, 2026 at 6:32 PM UTC", "If you don't know Jane Smith or didn't expect this invitation, ignore this email.",
		"Accept invitation", "Jane Smith invited new@example.com to Everstack.")
	if strings.Count(html, "https://app.everstack.ai/invite/tok") != 2 {
		t.Error("invite link should appear in the button and the fallback once each")
	}

	// Self-hosted invitations carry no organization, and an inviter may have
	// no name: the copy still reads.
	subject, html = renderAccount(t, types.EmailTypeInvitation,
		invitationData("new@example.com", "", "", "jane@acme.com", "https://gw.example.com/auth/invite?token=t", "", time.Time{}))
	assertClean(t, subject, html)
	if subject != "jane@acme.com invited you to their team on Everstack" {
		t.Errorf("subject = %q", subject)
	}
	text := visibleText(html)
	for _, banned := range []string{"Organization", "Role", "Expires", "the  organization"} {
		if strings.Contains(text, banned) {
			t.Errorf("empty value rendered: %q in %s", banned, text)
		}
	}
	assertText(t, "invitation without org", html, "Join your team on Everstack", "Invited by jane@acme.com", "jane@acme.com invited you to join their team on Everstack.")
}

// The Cloud welcome states the signup credit and its 30-day expiry; the
// self-hosted welcome (no credit) never mentions one.
func TestWelcomeEmailSignupCredit(t *testing.T) {
	data := welcomeData("sam@example.com", "Sam")
	data["starter_credit"] = formatWholeUSD(5)
	subject, html := renderAccount(t, types.EmailTypeWelcome, data)
	assertClean(t, subject, html)
	assertText(t, "cloud welcome", html, "Your account includes $5 of usage credit to try Everstack. It expires 30 days after signup.")

	_, plain := renderAccount(t, types.EmailTypeWelcome, welcomeData("sam@example.com", "Sam"))
	if strings.Contains(visibleText(plain), "credit") {
		t.Error("the welcome without a signup credit mentions credit")
	}
	if got := formatWholeUSD(2.5); got != "$2.50" {
		t.Errorf("formatWholeUSD(2.5) = %q", got)
	}
}

func TestWelcomeEmail(t *testing.T) {
	for _, name := range []string{"Sam Riley", ""} {
		subject, html := renderAccount(t, types.EmailTypeWelcome, welcomeData("sam@example.com", name))
		assertClean(t, subject, html)
		greeting := "Hi there, welcome to Everstack"
		if name != "" {
			greeting = "Hi Sam Riley, welcome to Everstack"
		}
		assertText(t, "welcome", html, greeting, "Connect a provider.", "Open Everstack", "Founder, Everstack",
			"You are receiving this because you signed up for Everstack at https://app.everstack.ai as sam@example.com.")
		if strings.Contains(visibleText(html), " - ") {
			t.Error("welcome uses a spaced hyphen as a dash")
		}
	}
}
