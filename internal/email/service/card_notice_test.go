package service

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/templates"
	"github.com/everstacklabs/everstack/internal/email/types"
)

func mustTemplate(t *testing.T, typ types.EmailType) *templates.EmailTemplate {
	t.Helper()
	tpl, err := templates.GetTemplate(typ)
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

func TestLogoDarkURL(t *testing.T) {
	def := config.DefaultConfig()
	if got := logoDarkURL(def, config.DefaultLogoURL); got != config.DefaultLogoDarkURL {
		t.Errorf("default logo: dark = %q", got)
	}
	if got := logoDarkURL(def, "https://example.com/our-logo.png"); got != "" {
		t.Errorf("custom logo without a dark variant: dark = %q, want none", got)
	}
	custom := config.DefaultConfig()
	custom.LogoDarkURL = "https://example.com/our-logo-light.png"
	if got := logoDarkURL(custom, "https://example.com/our-logo.png"); got != custom.LogoDarkURL {
		t.Errorf("configured dark logo ignored: %q", got)
	}
}

var noticeAt = time.Date(2026, 10, 4, 18, 32, 0, 0, time.UTC)

// pmKinds are the three ways a saved method is named.
var pmKinds = []struct {
	name   string
	id     PaymentMethodIdentity
	label  string
	isCard bool
}{
	{"card", PaymentMethodIdentity{Type: "card", Brand: "visa", Last4: "4242"}, "Visa ending in 4242", true},
	{"link", PaymentMethodIdentity{Type: "link", LinkEmail: "ada.lovelace@example.com"}, "Link (a***@example.com)", false},
	{"wallet", PaymentMethodIdentity{Type: "card", Brand: "visa", Last4: "4242", Wallet: "apple_pay"}, "Apple Pay (Visa ending in 4242)", false},
}

func noticeFor(kind int, event CardNoticeEvent) CardNoticeParams {
	k := pmKinds[kind]
	label, isCard := PaymentMethodLabel(k.id)
	p := CardNoticeParams{
		OrgName: "everstack", OrgSlug: "everstack", Event: event,
		Type: k.id.Type, Brand: k.id.Brand, Last4: k.id.Last4,
		MethodLabel: label, IsCard: isCard, ChangedAt: noticeAt,
	}
	if k.id.Last4 != "" {
		p.ExpMonth, p.ExpYear = 8, 2029
	}
	switch event {
	case CardNoticeAdded:
		p.IsDefault = true
	case CardNoticeRemoved:
		p.Source, p.ActorName = CardNoticeSourceMember, "Olive Owner"
		p.RemainingKnown, p.RemainingCount, p.RemainingDefault = true, 1, "Mastercard ending in 4444"
	case CardNoticeDefaultChanged:
		p.Source, p.ActorName = CardNoticeSourceMember, "Olive Owner"
		p.PreviousDefault = "Mastercard ending in 4444"
	case CardNoticeExpiring:
		p.ChangedAt = time.Time{}
	}
	return p
}

// visibleText strips tags and the head, collapsing whitespace, so copy
// assertions read like what the recipient sees.
func visibleText(html string) string {
	html = regexp.MustCompile(`(?s)<head>.*</head>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`</?(strong|a|span)\b[^>]*>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " ")
	html = strings.NewReplacer("&#39;", "'", "&amp;", "&", "&#43;", "+").Replace(html)
	return strings.Join(strings.Fields(html), " ")
}

func assertClean(t *testing.T, subject, html string) {
	t.Helper()
	for _, banned := range []string{"\u2014", "\u2013", "idfusion", "<no value>", "removed from everstack", "added to everstack", "Your Link payment method"} {
		if strings.Contains(subject+html, banned) {
			t.Errorf("rendered email contains %q", banned)
		}
	}
	// The org is never a bare grey subtitle under the headline any more.
	if strings.Contains(html, `<p style="color: #71717a; font-size: 14px; margin: 0 0 24px;">`) {
		t.Error("rendered email still has the bare organization subtitle")
	}
}

func TestPaymentMethodEmailsNameMethodAndOrganization(t *testing.T) {
	events := []struct {
		event     CardNoticeEvent
		typ       types.EmailType
		headline  map[bool]string // isCard -> headline
		subject   string          // %s = label
		body      string
		security  bool
		extraText []string
	}{
		{CardNoticeAdded, types.EmailTypeCardAdded, map[bool]string{true: "Card added", false: "Payment method added"},
			"%s added to the everstack organization", "%s was added to the everstack organization on Everstack. It is the default payment method", true,
			[]string{"Added Oct 4, 2026 at 6:32 PM UTC"}},
		{CardNoticeRemoved, types.EmailTypeCardRemoved, map[bool]string{true: "Card removed", false: "Payment method removed"},
			"%s removed from the everstack organization", "%s was removed from the everstack organization on Everstack and will not be charged again.", true,
			[]string{"Removed by Olive Owner", "Removed Oct 4, 2026 at 6:32 PM UTC", "Your default payment method is now Mastercard ending in 4444."}},
		{CardNoticeDefaultChanged, types.EmailTypeCardDefaultChanged, map[bool]string{true: "Default payment method changed", false: "Default payment method changed"},
			"Default payment method changed for the everstack organization", "%s is now the default payment method for the everstack organization on Everstack.", true,
			[]string{"New default %s", "Previous default Mastercard ending in 4444", "Changed by Olive Owner", "Changed Oct 4, 2026 at 6:32 PM UTC"}},
		{CardNoticeExpiring, types.EmailTypeCardExpiring, map[bool]string{true: "Card expiring soon", false: "Payment method expiring soon"},
			"%s for the everstack organization expires soon", "%s, the default payment method for the everstack organization on Everstack, expires", false, nil},
	}
	for _, ev := range events {
		for i, k := range pmKinds {
			t.Run(string(ev.event)+"/"+k.name, func(t *testing.T) {
				if got := cardNoticeTypes[ev.event]; got != ev.typ {
					t.Fatalf("type = %s, want %s", got, ev.typ)
				}
				label, isCard := PaymentMethodLabel(k.id)
				if label != k.label || isCard != k.isCard {
					t.Fatalf("label = %q card=%v, want %q card=%v", label, isCard, k.label, k.isCard)
				}
				subject, html := renderBilling(t, ev.typ, cardNoticeData(noticeFor(i, ev.event)))
				text := visibleText(html)
				if want := strings.ReplaceAll(ev.subject, "%s", label); subject != want {
					t.Errorf("subject = %q, want %q", subject, want)
				}
				wants := []string{
					ev.headline[isCard],
					"Organization everstack",
					strings.ReplaceAll(ev.body, "%s", label),
					"Manage payment methods",
					"you manage billing for the everstack organization on Everstack",
				}
				for _, x := range ev.extraText {
					wants = append(wants, strings.ReplaceAll(x, "%s", label))
				}
				if ev.event != CardNoticeDefaultChanged {
					wants = append(wants, "Payment method "+label)
				}
				for _, want := range wants {
					if !strings.Contains(text, want) {
						t.Errorf("body missing %q\n%s", want, text)
					}
				}
				const security = "If you didn't make this change, remove access for anyone you don't recognise and contact us at billing@everstack.ai."
				if got := strings.Contains(text, security); got != ev.security {
					t.Errorf("security line present = %v, want %v", got, ev.security)
				}
				if !strings.Contains(html, "https://app.everstack.ai/everstack/settings/billing") {
					t.Error("missing org billing link")
				}
				if !isCard && strings.Contains(text, "Card added") {
					t.Error("non-card headline says Card")
				}
				assertClean(t, subject, html)
				if config.DefaultConfig().SenderFor(ev.typ) != config.DefaultConfig().Senders.Billing {
					t.Errorf("%s is not sent from the billing sender", ev.typ)
				}
			})
		}
	}
}

func TestRemovedEmailAttributionAndRemaining(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*CardNoticeParams)
		want    []string
		notWant []string
	}{
		{"member", func(p *CardNoticeParams) {}, []string{"Removed by Olive Owner"}, []string{"outside your", "automatically"}},
		{"member without a resolvable name", func(p *CardNoticeParams) { p.ActorName = "" },
			[]string{"Removed by A member of everstack"}, nil},
		{"stripe", func(p *CardNoticeParams) { p.Source, p.ActorName = CardNoticeSourceStripe, "" },
			[]string{"It was removed outside your Everstack billing page."}, []string{"Olive", "Removed by", "Stripe"}},
		{"automatic", func(p *CardNoticeParams) { p.Source, p.ActorName = CardNoticeSourceSync, "" },
			[]string{"It was removed automatically because it is no longer on file for this organization."}, []string{"Removed by", "Stripe"}},
		{"no methods left", func(p *CardNoticeParams) { p.RemainingCount, p.RemainingDefault = 0, "" },
			[]string{"You have no saved payment method. Credit top-ups will ask for a card at checkout."}, []string{"default payment method is now"}},
		{"methods left but no default", func(p *CardNoticeParams) { p.RemainingCount, p.RemainingDefault = 2, "" },
			[]string{"You still have 2 saved payment methods, but none is the default."}, nil},
		{"older notice without context", func(p *CardNoticeParams) {
			p.Source, p.ActorName, p.RemainingKnown, p.ChangedAt = "", "", false, time.Time{}
		}, []string{"Visa ending in 4242 was removed from the everstack organization on Everstack"},
			[]string{"Removed by", "default payment method is now", "no saved payment method"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := noticeFor(0, CardNoticeRemoved)
			c.mutate(&p)
			subject, html := renderBilling(t, types.EmailTypeCardRemoved, cardNoticeData(p))
			text := visibleText(html)
			for _, w := range c.want {
				if !strings.Contains(text, w) {
					t.Errorf("missing %q\n%s", w, text)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(text, w) {
					t.Errorf("unexpected %q\n%s", w, text)
				}
			}
			assertClean(t, subject, html)
		})
	}
}

func TestDefaultChangedAttribution(t *testing.T) {
	for _, src := range []CardNoticeSource{CardNoticeSourceStripe, CardNoticeSourceSync} {
		p := noticeFor(0, CardNoticeDefaultChanged)
		p.Source, p.ActorName = src, ""
		_, html := renderBilling(t, types.EmailTypeCardDefaultChanged, cardNoticeData(p))
		text := visibleText(html)
		if !strings.Contains(text, "The default was changed outside your Everstack billing page.") {
			t.Errorf("%s: missing the plain-language note\n%s", src, text)
		}
		if strings.Contains(text, "Changed by") || strings.Contains(text, "Stripe") {
			t.Errorf("%s: names a vendor or a non-member actor\n%s", src, text)
		}
	}
}

// A notice queued by the previous release (no label, no source) still
// renders, naming the method from its brand and last four.
func TestLegacyNoticesFallBack(t *testing.T) {
	_, html := renderBilling(t, types.EmailTypeCardRemoved, cardNoticeData(CardNoticeParams{
		OrgName: "Acme", Event: CardNoticeRemoved, Type: "link",
	}))
	if text := visibleText(html); !strings.Contains(text, "Payment method removed") || !strings.Contains(text, "Link was removed from the Acme organization") {
		t.Fatalf("legacy link notice: %s", text)
	}
	subject, html := renderBilling(t, types.EmailTypeCardRemoved, cardNoticeData(CardNoticeParams{
		OrgName: "Acme", Event: CardNoticeRemoved, Type: "card", Brand: "newbrand", Last4: "0005",
	}))
	if subject != "Card ending in 0005 removed from the Acme organization" || !strings.Contains(visibleText(html), "Card removed") {
		t.Fatalf("legacy card notice: %q %s", subject, visibleText(html))
	}
	_, html = renderBilling(t, types.EmailTypeCardExpiring, cardNoticeData(CardNoticeParams{OrgName: "Acme", Event: CardNoticeExpiring, Type: "card", Last4: "1"}))
	if text := visibleText(html); !strings.Contains(text, "expires soon") || strings.Contains(text, "Expires ") {
		t.Fatal("expiry row or fallback copy wrong when expiry is unknown")
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"ada.lovelace@example.com": "a***@example.com",
		"x@y.io":                   "x***@y.io",
		" Ada@Example.com ":        "A***@Example.com",
		"élise@exemple.fr":         "é***@exemple.fr",
		"not-an-address":           "",
		"@gmail.com":               "",
		"ada@":                     "",
		"":                         "",
	} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
	if label, _ := PaymentMethodLabel(PaymentMethodIdentity{Type: "link"}); label != "Link" {
		t.Errorf("link without email = %q", label)
	}
	if label, isCard := PaymentMethodLabel(PaymentMethodIdentity{Type: "card", Brand: "mastercard", Last4: "4444", Wallet: "google_pay"}); label != "Google Pay (Mastercard ending in 4444)" || isCard {
		t.Errorf("google pay = %q %v", label, isCard)
	}
}
