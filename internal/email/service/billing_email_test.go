package service

import (
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/email/templates"
	"github.com/everstacklabs/everstack/internal/email/types"
)

func TestBillingURL(t *testing.T) {
	cases := map[[2]string]string{
		{"https://app.everstack.ai", "acme"}:   "https://app.everstack.ai/acme/settings/billing",
		{"https://app.everstack.ai/", "/acme"}: "https://app.everstack.ai/acme/settings/billing",
		{"https://app.everstack.ai", ""}:       "https://app.everstack.ai",
		{"", "acme"}:                           "https://app.everstack.ai/acme/settings/billing",
	}
	for in, want := range cases {
		if got := BillingURL(in[0], in[1]); got != want {
			t.Errorf("BillingURL(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := []struct {
		amount   float64
		currency string
		want     string
	}{
		{12.5, "usd", "$12.50"},
		{12.5, "", "$12.50"},
		{3, "EUR", "€3.00"},
		{0.99, "gbp", "£0.99"},
		{100, "chf", "100.00 CHF"},
		{1000, "jpy", "1000 JPY"},
		{1.23, "kwd", "1.230 KWD"},
	}
	for _, c := range cases {
		if got := FormatMoney(c.amount, c.currency); got != c.want {
			t.Errorf("FormatMoney(%v,%q) = %q, want %q", c.amount, c.currency, got, c.want)
		}
	}
}

// renderBilling mirrors what SendEmail injects before rendering, so the
// assertions run against the exact HTML a customer would see.
func renderBilling(t *testing.T, typ types.EmailType, data map[string]interface{}) (string, string) {
	t.Helper()
	tpl, err := templates.GetTemplate(typ)
	if err != nil {
		t.Fatal(err)
	}
	data["cloud_url"] = "https://app.everstack.ai"
	data["logo_url"] = "https://everstack.ai/logo.png"
	data["docs_url"] = "https://docs.everstack.ai"
	slug, _ := data["org_slug"].(string)
	data["billing_url"] = BillingURL("https://app.everstack.ai", slug)
	subject, html, err := tpl.Render(data)
	if err != nil {
		t.Fatal(err)
	}
	return subject, html
}

func TestBillingTemplatesLinkToTheOrgBillingPage(t *testing.T) {
	const want = "https://app.everstack.ai/acme/settings/billing"
	for _, typ := range []types.EmailType{types.EmailTypePaymentSuccess, types.EmailTypePaymentFailed, types.EmailTypeUsageWarning, types.EmailTypeCreditLowBalance} {
		_, html := renderBilling(t, typ, map[string]interface{}{
			"organization_name": "Acme", "org_slug": "acme", "amount": "$12.00",
			"usage_percent": "80", "available": "$1.00", "funded": "$10.00", "threshold_percent": "10", "remaining_percent": "10",
		})
		if !strings.Contains(html, want) {
			t.Errorf("%s: billing link %q missing", typ, want)
		}
		if strings.Contains(html, "app.everstack.ai/settings/billing") {
			t.Errorf("%s: still links to the slug-less billing path (404 in the cloud app)", typ)
		}
	}
}

func TestReceiptRendersFormattedAmountOnce(t *testing.T) {
	subject, html := renderBilling(t, types.EmailTypePaymentSuccess, map[string]interface{}{
		"organization_name": "Acme", "org_slug": "acme", "amount": "€12.00", "invoice_number": "INV-1", "invoice_url": "https://invoice.stripe.com/i/x",
	})
	if !strings.Contains(subject, "€12.00") || strings.Contains(html, "$€") {
		t.Fatalf("amount rendering wrong: subject=%q", subject)
	}
	if !strings.Contains(html, "https://invoice.stripe.com/i/x") {
		t.Fatal("hosted invoice link missing")
	}
}

func TestPaymentFailedPrefersDirectPaymentLinkAndHidesEmptyReason(t *testing.T) {
	_, html := renderBilling(t, types.EmailTypePaymentFailed, map[string]interface{}{
		"organization_name": "Acme", "org_slug": "acme", "amount": "$49.00",
		"payment_url": "https://invoice.stripe.com/i/in_failed", "next_retry_at": "Oct 3 at 2:00 PM UTC",
	})
	if !strings.Contains(html, `href="https://invoice.stripe.com/i/in_failed"`) {
		t.Fatal("CTA must go to the hosted invoice when we have one")
	}
	if strings.Contains(html, "Reason from your bank") {
		t.Fatal("reason panel rendered with no reason")
	}
	if strings.Contains(html, "services will be paused") {
		t.Fatal("grace-period claim rendered without a grace period")
	}
	if !strings.Contains(html, "Oct 3 at 2:00 PM UTC") {
		t.Fatal("next retry line missing")
	}
	_, html = renderBilling(t, types.EmailTypePaymentFailed, map[string]interface{}{"organization_name": "Acme", "org_slug": "acme"})
	if !strings.Contains(html, `href="https://app.everstack.ai/acme/settings/billing"`) {
		t.Fatal("CTA must fall back to the billing page without a payment link")
	}
}

func TestLowBalanceTemplateHasWarningAndDepletedVariants(t *testing.T) {
	subject, html := renderBilling(t, types.EmailTypeCreditLowBalance, map[string]interface{}{
		"organization_name": "Acme", "org_slug": "acme", "available": "$2.00", "funded": "$10.00",
		"threshold_percent": "25", "remaining_percent": "20", "depleted": false,
	})
	if !strings.Contains(subject, "has $2.00 of Everstack credit left") || !strings.Contains(html, "running low") || strings.Contains(html, "used up") {
		t.Fatalf("warning variant wrong: subject=%q", subject)
	}
	subject, html = renderBilling(t, types.EmailTypeCreditLowBalance, map[string]interface{}{
		"organization_name": "Acme", "org_slug": "acme", "available": "$0.00", "funded": "$10.00",
		"threshold_percent": "0", "remaining_percent": "0", "depleted": true,
	})
	if !strings.Contains(subject, "out of Everstack credit") || !strings.Contains(html, "used up") || !strings.Contains(html, "stop until credit is added") {
		t.Fatalf("depleted variant wrong: subject=%q", subject)
	}
	for _, s := range []string{subject, html} {
		if strings.Contains(s, "\u2014") {
			t.Fatal("em dash in customer copy")
		}
	}
}

func TestMajorUnitsUsesCurrencyMinorUnit(t *testing.T) {
	cases := []struct {
		minor    int64
		currency string
		want     float64
	}{
		{1000, "usd", 10},
		{1000, "", 10},
		{1000, "JPY", 1000},
		{1230, "kwd", 1.23},
		{500, "eur", 5},
	}
	for _, c := range cases {
		if got := MajorUnits(c.minor, c.currency); got != c.want {
			t.Errorf("MajorUnits(%d,%q) = %v, want %v", c.minor, c.currency, got, c.want)
		}
	}
}

func TestInstanceLimitTemplatesRenderEveryStage(t *testing.T) {
	grace := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	params := func(stage InstanceLimitStage) InstanceLimitParams {
		return InstanceLimitParams{
			OrgName: "Acme", OrgSlug: "acme", Stage: stage, InstanceLimit: 1, SlotsUsed: 2, GraceEndsAt: grace,
			KeptSlugs: []string{"staging"}, PausedSlugs: []string{"dev"}, ExtraInstancePriceUSD: 5,
			NoticedAt: time.Date(2026, 10, 4, 18, 32, 0, 0, time.UTC),
		}
	}
	render := func(typ types.EmailType, stage InstanceLimitStage) (string, string) {
		return renderBilling(t, typ, instanceLimitData(params(stage), "https://app.everstack.ai/acme/instances"))
	}
	shared := []string{"Organization Acme", "Plan allows 1 instance",
		"Extra instance cost $5.00 per extra instance per month, from prepaid credit", "Choose which instances to keep",
		"upgrade the plan or pay for extra instances", "you manage billing for the Acme organization on Everstack"}

	subject, html := render(types.EmailTypeInstanceOverLimit, InstanceLimitStageOverLimit)
	if subject != "The Acme organization is over its Everstack instance limit" {
		t.Errorf("T0 subject = %q", subject)
	}
	assertText(t, "T0", html, append(shared, "Running now 2", "Over the plan's instance limit", "Deadline Oct 7, 2026 at 12:00 PM UTC",
		"Noticed Oct 4, 2026 at 6:32 PM UTC",
		"The Acme organization on Everstack has 2 instances running, and its plan allows 1 instance.",
		"Everything keeps running until Oct 7, 2026 at 12:00 PM UTC")...)

	subject, html = render(types.EmailTypeInstanceOverLimitReminder, InstanceLimitStageReminder)
	if !strings.HasPrefix(subject, "Two days left: the Acme organization") {
		t.Errorf("reminder subject = %q", subject)
	}
	assertText(t, "reminder", html, append(shared, "Two days left to choose which instances to keep", "paused automatically", "Reminder sent Oct 4")...)

	subject, html = render(types.EmailTypeInstancesPaused, InstanceLimitStagePaused)
	if subject != "Everstack paused dev for the Acme organization" {
		t.Errorf("paused subject = %q", subject)
	}
	assertText(t, "paused", html, append(shared, "Instances paused", "Paused instances dev", "Still running staging",
		"Paused Oct 4, 2026 at 6:32 PM UTC",
		"Everstack paused dev for the Acme organization because it was still over its plan's limit of 1 instance at the deadline.")...)

	for _, typ := range []types.EmailType{types.EmailTypeInstanceOverLimit, types.EmailTypeInstanceOverLimitReminder, types.EmailTypeInstancesPaused} {
		subject, html := renderBilling(t, typ, instanceLimitData(InstanceLimitParams{OrgName: "Acme", OrgSlug: "acme", InstanceLimit: 3, SlotsUsed: 4, GraceEndsAt: grace}, "https://app.everstack.ai/acme/instances"))
		assertClean(t, subject, html)
		if !strings.Contains(html, `href="https://app.everstack.ai/acme/instances"`) || !strings.Contains(html, `href="https://app.everstack.ai/acme/settings/billing"`) {
			t.Fatalf("%s must link to the instances page and the billing page", typ)
		}
		// An older notice has no price or time: those rows hide.
		text := visibleText(html)
		if strings.Contains(text, "Extra instance cost") || strings.Contains(text, "Noticed") || !strings.Contains(text, "Plan allows 3 instances") {
			t.Errorf("%s: legacy render = %s", typ, text)
		}
	}
}

// Sent to an organization's other owners when its billing email followed an
// owner's sign-in email change.
func TestBillingContactChangedNotice(t *testing.T) {
	changed := time.Date(2026, 10, 7, 18, 32, 0, 0, time.UTC)
	subject, html := renderBilling(t, types.EmailTypeBillingContactChanged, billingContactChangedData(BillingContactChangedParams{
		To: "bo@acme.com", OrgName: "Acme", OrgSlug: "acme", MemberName: "Ana Lima",
		OldEmail: "ana@old.example", NewEmail: "ana@new.example", ChangedAt: changed,
	}))
	if subject != "The billing email for the Acme organization changed" {
		t.Errorf("subject = %q", subject)
	}
	assertText(t, "billing contact", html,
		"Billing email changed", "Organization Acme", "New billing email ana@new.example",
		"Previous billing email ana@old.example", "Changed Oct 7, 2026 at 6:32 PM UTC",
		"Ana Lima changed their Everstack sign-in email.",
		"Receipts and billing notices for the Acme organization now go to ana@new.example.",
		"Open billing settings")
	if !strings.Contains(html, "https://app.everstack.ai/acme/settings/billing") && !strings.Contains(html, BillingURL("https://app.everstack.ai", "acme")) {
		t.Error("the button should open the organization's billing settings")
	}

	// Without a name the copy still reads.
	_, html = renderBilling(t, types.EmailTypeBillingContactChanged, billingContactChangedData(BillingContactChangedParams{
		To: "bo@acme.com", OrgName: "Acme", OrgSlug: "acme", OldEmail: "a@x.co", NewEmail: "b@x.co", ChangedAt: changed,
	}))
	assertText(t, "billing contact, no name", html, "An owner changed their Everstack sign-in email.")
}
