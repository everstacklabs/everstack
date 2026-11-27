package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/types"
)

// renderLifecycle renders a lifecycle email from the same data map its Send
// method hands the provider, so a key the builder forgets shows up here as a
// missing string rather than a blank row in a customer's inbox.
func renderLifecycle(t *testing.T, typ types.EmailType, data map[string]interface{}) (string, string) {
	t.Helper()
	return renderBilling(t, typ, data)
}

func assertCustomerCopy(t *testing.T, typ types.EmailType, subject, html string) {
	t.Helper()
	for _, s := range []string{subject, html} {
		if strings.Contains(s, "—") {
			t.Errorf("%s: em dash in customer copy", typ)
		}
		if strings.Contains(strings.ToLower(s), "midfu"+"sion") {
			t.Errorf("%s: legacy brand name in customer copy", typ)
		}
		if strings.Contains(s, "<no value>") {
			t.Errorf("%s: template references a key the builder does not set", typ)
		}
	}
	if !strings.Contains(html, `href="https://app.everstack.ai/acme/settings/billing"`) {
		t.Errorf("%s: must link to the org billing page", typ)
	}
}

func TestSubscriptionStartedEmailForAZeroDollarCloudPlan(t *testing.T) {
	data := subscriptionStartedData(SubscriptionStartedParams{
		OrgName: "Acme", OrgSlug: "acme", PlanName: "Starter", Price: "$0", BillingPeriod: "monthly",
		SeatLimit: 1, InstanceLimit: 1, IncludedCredit: 5, CreditOneTime: true,
		PaymentMethod: "Visa ending in 4242", PeriodEnd: "Nov 2, 2026",
	})
	subject, html := renderLifecycle(t, types.EmailTypeSubscriptionStarted, data)
	assertCustomerCopy(t, types.EmailTypeSubscriptionStarted, subject, html)
	if subject != "The Acme organization is on the Everstack Starter plan" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{"$0 / month", "Visa ending in 4242", ">1 instance<", ">1 seat<", "Nov 2, 2026"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	// A Cloud plan includes its limits, not credit: no included credit line,
	// even if a caller passes an amount.
	if strings.Contains(html, "Included credit") || strings.Contains(html, "$5.00") {
		t.Fatal("cloud plan must not state included credit")
	}
	if strings.Contains(html, "self-hosted") || strings.Contains(strings.ToLower(html), "activation") {
		t.Fatal("cloud plan must not carry the self-hosted activation section")
	}
}

func TestSubscriptionStartedEmailForSelfHostedLicenseLinksInsteadOfTheToken(t *testing.T) {
	data := subscriptionStartedData(SubscriptionStartedParams{
		OrgName: "Acme", OrgSlug: "acme", PlanName: "Scale", Price: "$1000", BillingPeriod: "yearly",
		SeatLimit: 10, InstanceLimit: 10, IncludedCredit: 50, SelfHosted: true,
	})
	subject, html := renderLifecycle(t, types.EmailTypeSubscriptionStarted, data)
	assertCustomerCopy(t, types.EmailTypeSubscriptionStarted, subject, html)
	for _, want := range []string{"$1000 / year", "$50.00 each billing period", ">10 instances<", ">10 seats<", "self-hosted license", "never send activation tokens by email"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(html, "Payment method") {
		t.Fatal("payment method row rendered without a payment method")
	}
}

func TestSubscriptionChangedEmailRendersEveryChange(t *testing.T) {
	cases := []struct {
		change      SubscriptionChange
		wantSubject string
		wantText    []string
		security    bool
	}{
		{SubscriptionChangePlanChanged, "The Acme organization moved from Build to the Scale plan", []string{
			"Plan changed to Scale", "New plan Scale", "Previous plan Build", "Billing interval Yearly", "Price $200 / year",
			"Organization Acme", "Changed by Olive Owner", "Changed Oct 4, 2026 at 6:32 PM UTC",
			"The Acme organization on Everstack moved from Build to the Scale plan.", "any price difference shows on the next invoice",
		}, true},
		{SubscriptionChangeCancelScheduled, "The Scale plan for the Acme organization ends on Nov 2, 2026", []string{
			"Scale plan set to end", "Plan Scale", "Ends on Nov 2, 2026", "Scheduled by Olive Owner", "Scheduled Oct 4, 2026 at 6:32 PM UTC",
			"is set to end on Nov 2, 2026.", "moves to the Starter plan",
		}, true},
		{SubscriptionChangeResumed, "The Scale plan for the Acme organization will keep renewing", []string{
			"Scale plan will keep renewing", "Changed by Olive Owner", "was withdrawn.", "The plan renews as before.",
		}, true},
		{SubscriptionChangeEnded, "The Scale plan for the Acme organization has ended", []string{
			"Scale plan ended", "Ended Oct 4, 2026 at 6:32 PM UTC", "has ended.", "on the Starter plan now",
		}, false},
	}
	for _, c := range cases {
		data := subscriptionChangedData(SubscriptionChangedParams{
			OrgName: "Acme", OrgSlug: "acme", Change: c.change, PlanName: "Scale", PreviousPlanName: "Build", EffectiveDate: "Nov 2, 2026",
			BillingPeriod: "yearly", Price: "$200", ChangedAt: noticeAt, ActorName: "Olive Owner",
		})
		subject, html := renderLifecycle(t, types.EmailTypeSubscriptionChanged, data)
		assertCustomerCopy(t, types.EmailTypeSubscriptionChanged, subject, html)
		assertClean(t, subject, html)
		if subject != c.wantSubject {
			t.Errorf("%s: subject = %q, want %q", c.change, subject, c.wantSubject)
		}
		text := visibleText(html)
		for _, want := range c.wantText {
			if !strings.Contains(text, want) {
				t.Errorf("%s: missing %q\n%s", c.change, want, text)
			}
		}
		if got := strings.Contains(text, "If you didn't make this change"); got != c.security {
			t.Errorf("%s: security line = %v, want %v", c.change, got, c.security)
		}
	}
}

// A change nobody can be named for (a Stripe portal or dashboard change, or
// a notice queued by an older release) shows no "by" row rather than a guess.
func TestSubscriptionChangedWithoutAnActorOmitsTheRow(t *testing.T) {
	for _, change := range []SubscriptionChange{SubscriptionChangePlanChanged, SubscriptionChangeCancelScheduled, SubscriptionChangeResumed} {
		data := subscriptionChangedData(SubscriptionChangedParams{OrgName: "Acme", OrgSlug: "acme", Change: change, PlanName: "Scale"})
		_, html := renderLifecycle(t, types.EmailTypeSubscriptionChanged, data)
		text := visibleText(html)
		for _, banned := range []string{"Changed by", "Scheduled by", "Billing interval", "Price"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s: %q rendered without a value", change, banned)
			}
		}
	}
}

func TestSubscriptionStartedNamesWhoAndWhen(t *testing.T) {
	data := subscriptionStartedData(SubscriptionStartedParams{
		OrgName: "Acme", OrgSlug: "acme", PlanName: "Pro", Price: "$20", BillingPeriod: "monthly",
		StartedAt: noticeAt, ActorName: "Olive Owner",
	})
	subject, html := renderLifecycle(t, types.EmailTypeSubscriptionStarted, data)
	assertClean(t, subject, html)
	text := visibleText(html)
	for _, want := range []string{"Pro plan started", "Billing interval Monthly", "Price $20 / month", "Organization Acme",
		"Started by Olive Owner", "Started Oct 4, 2026 at 6:32 PM UTC",
		"The Pro plan is now active for the Acme organization on Everstack.", "If you didn't make this change", "Open billing",
		"you manage billing for the Acme organization on Everstack"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q\n%s", want, text)
		}
	}
}

func TestCreditPurchaseReceiptEmail(t *testing.T) {
	data := creditPurchaseData(CreditPurchaseParams{
		OrgName: "Acme", OrgSlug: "acme", CreditAdded: 25, Tax: 2.13, Total: 27.13, Currency: "usd",
		Balance: 31.5, BalanceKnown: true, InvoiceURL: "https://invoice.stripe.com/i/in_top",
	})
	subject, html := renderLifecycle(t, types.EmailTypeCreditPurchased, data)
	assertCustomerCopy(t, types.EmailTypeCreditPurchased, subject, html)
	if subject != "Receipt: $25.00 of credit added to the Acme organization" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{"$2.13", "$27.13", "$31.50", `href="https://invoice.stripe.com/i/in_top"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	data = creditPurchaseData(CreditPurchaseParams{OrgName: "Acme", OrgSlug: "acme", CreditAdded: 10, Total: 10, Currency: "usd"})
	_, html = renderLifecycle(t, types.EmailTypeCreditPurchased, data)
	if strings.Contains(html, "New balance") || strings.Contains(html, "View invoice") || strings.Contains(html, ">Tax<") {
		t.Fatal("optional rows must hide when their value is unknown")
	}
}

func TestCreditReversalEmailRendersEveryKind(t *testing.T) {
	cases := []struct {
		kind        CreditReversalKind
		wantSubject string
		wantBody    string
	}{
		{CreditReversalRefund, "Refund issued: $10.00 of credit removed from the Acme organization", "$12.00 USD was refunded"},
		{CreditReversalDisputeOpened, "Payment disputed: $10.00 of credit on hold for the Acme organization", "on hold"},
		{CreditReversalDisputeWon, "Dispute resolved: $10.00 of credit released for the Acme organization", "released"},
		{CreditReversalDisputeLost, "Dispute closed: $10.00 of credit removed from the Acme organization", "removed"},
		{CreditReversalRefundFailed, "Refund failed: $10.00 of credit restored to the Acme organization", "$12.00 USD refund to the original payment method did not go through"},
	}
	for _, c := range cases {
		data := creditReversalData(CreditReversalParams{OrgName: "Acme", OrgSlug: "acme", Kind: c.kind, Amount: 12, Currency: "usd", Credit: 10})
		subject, html := renderLifecycle(t, types.EmailTypeCreditReversed, data)
		assertCustomerCopy(t, types.EmailTypeCreditReversed, subject, html)
		if subject != c.wantSubject {
			t.Errorf("%s: subject = %q, want %q", c.kind, subject, c.wantSubject)
		}
		if !strings.Contains(html, c.wantBody) {
			t.Errorf("%s: body missing %q", c.kind, c.wantBody)
		}
	}
}

func TestLifecycleEmailsSendFromBilling(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, typ := range []types.EmailType{
		types.EmailTypeSubscriptionStarted, types.EmailTypeSubscriptionChanged,
		types.EmailTypeCreditPurchased, types.EmailTypeCreditReversed,
		types.EmailTypeAutoTopUpCharged, types.EmailTypeAutoTopUpFailed,
	} {
		if got, want := cfg.SenderFor(typ), cfg.Senders.Billing; got != want {
			t.Errorf("%s sends as %+v, want the billing identity", typ, got)
		}
	}
}

func TestEmailSendFailureLogPrivacy(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{{"resend API error (422): map[email:private-local@example.test body:secret]", "provider status 422"}, {"private-local@example.test secret", "provider error"}} {
		line := emailSendFailureLog(types.EmailTypeFounderNote, "private-local@example.test", errors.New(tt.raw))
		if strings.Contains(line, "private-local") || strings.Contains(line, "secret") || !strings.Contains(line, "domain=example.test") || !strings.Contains(line, tt.want) {
			t.Fatalf("unsafe log: %s", line)
		}
	}
}
