package service

import (
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/types"
)

func assertText(t *testing.T, name, html string, wants ...string) {
	t.Helper()
	text := visibleText(html)
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s: missing %q\n%s", name, want, text)
		}
	}
}

func TestCreditReceiptNamesAmountBuyerTimeAndReference(t *testing.T) {
	for _, approved := range []bool{false, true} {
		p := CreditPurchaseParams{
			OrgName: "Acme", OrgSlug: "acme", CreditAdded: 25, Tax: 2.13, Total: 27.13, Currency: "usd",
			Balance: 31.5, BalanceKnown: true, PaidAt: noticeAt, ActorName: "Olive Owner", Approved: approved,
		}
		if !approved {
			p.InvoiceURL = "https://invoice.stripe.com/i/in_top"
		}
		subject, html := renderBilling(t, types.EmailTypeCreditPurchased, creditPurchaseData(p))
		assertClean(t, subject, html)
		assertCustomerCopy(t, types.EmailTypeCreditPurchased, subject, html)
		assertText(t, "receipt", html, "$25.00 of credit added", "Credit added $25.00", "Tax $2.13", "Total charged $27.13 USD",
			"New balance $31.50", "Organization Acme", "Purchased by Olive Owner", "Paid Oct 4, 2026 at 6:32 PM UTC",
			"$25.00 of prepaid credit was added to the Acme organization on Everstack",
			"Prepaid credit never expires", "If you didn't make this purchase")
		text := visibleText(html)
		if approved {
			assertText(t, "approved", html, "after we reviewed the payment", "held for review", "Open billing")
		} else {
			if strings.Contains(text, "review") {
				t.Error("checkout receipt mentions a review")
			}
			assertText(t, "invoice", html, "View invoice", "billing page")
		}
	}
}

func TestCreditReversalRowsPerKind(t *testing.T) {
	cases := []struct {
		kind  CreditReversalKind
		wants []string
	}{
		{CreditReversalRefund, []string{"Refund issued", "Refunded $12.00 USD", "Credit removed $10.00", "Issued by Everstack", "Issued Oct 4, 2026 at 6:32 PM UTC", "5 to 10 business days", "If you didn't ask for this refund"}},
		{CreditReversalDisputeOpened, []string{"A payment was disputed", "Disputed amount $12.00 USD", "Credit on hold $10.00", "Opened by Your bank", "Opened Oct 4", "cannot be spent while the bank reviews"}},
		{CreditReversalDisputeWon, []string{"Dispute resolved", "Credit released $10.00", "Decided by Your bank", "Resolved Oct 4", "available to spend again"}},
		{CreditReversalDisputeLost, []string{"Dispute closed", "Credit removed $10.00", "Decided by Your bank", "Closed Oct 4", "in the cardholder's favor"}},
		{CreditReversalRefundFailed, []string{"Refund failed", "Refund amount $12.00 USD", "Credit restored $10.00", "Failed Oct 4", "No money was returned"}},
	}
	for _, c := range cases {
		data := creditReversalData(CreditReversalParams{
			OrgName: "Acme", OrgSlug: "acme", Kind: c.kind, Amount: 12, Currency: "usd", Credit: 10,
			Balance: 40, BalanceKnown: true, At: noticeAt,
		})
		subject, html := renderBilling(t, types.EmailTypeCreditReversed, data)
		assertClean(t, subject, html)
		assertText(t, string(c.kind), html, append(c.wants, "New balance $40.00", "Organization Acme", "Open billing",
			"you manage billing for the Acme organization on Everstack")...)
		if c.kind != CreditReversalRefund && strings.Contains(visibleText(html), "If you didn't") {
			t.Errorf("%s: security line on a bank-initiated change", c.kind)
		}
	}
	// Older notices: no balance, time or reference rows.
	_, html := renderBilling(t, types.EmailTypeCreditReversed, creditReversalData(CreditReversalParams{OrgName: "Acme", OrgSlug: "acme", Kind: CreditReversalRefund, Amount: 12, Currency: "usd", Credit: 10}))
	for _, banned := range []string{"New balance", "Issued Oct"} {
		if strings.Contains(visibleText(html), banned) {
			t.Errorf("legacy reversal rendered %q", banned)
		}
	}
}

func TestReceiptAndLowBalanceFollowTheNoticePattern(t *testing.T) {
	subject, html := renderBilling(t, types.EmailTypePaymentSuccess, paymentSuccessData(PaymentSuccessParams{
		OrgName: "Acme", OrgSlug: "acme", Amount: 49, Currency: "usd", PlanTier: "Pro", BillingPeriod: "Sep 1 to Sep 30, 2026",
		InvoiceNumber: "INV-0042", InvoiceURL: "https://invoice.stripe.com/i/in_1", PaidAt: noticeAt,
	}))
	assertClean(t, subject, html)
	if subject != "Receipt: $49.00 USD paid by the Acme organization" {
		t.Errorf("subject = %q", subject)
	}
	assertText(t, "receipt", html, "Payment received", "Amount paid $49.00 USD", "Plan Pro", "Billing period Sep 1 to Sep 30, 2026",
		"Invoice INV-0042", "Organization Acme", "Paid Oct 4, 2026 at 6:32 PM UTC",
		"Everstack received $49.00 USD from the Acme organization for the Pro plan.", "If you don't recognise this charge",
		"View invoice", "billing page", "you manage billing for the Acme organization on Everstack")

	for _, depleted := range []bool{false, true} {
		p := LowBalanceParams{OrgName: "Acme", OrgSlug: "acme", Available: 2, Funded: 10, ThresholdPercent: 25, Currency: "usd", CheckedAt: noticeAt}
		if depleted {
			p.Available, p.ThresholdPercent, p.Depleted = 0, 0, true
		}
		subject, html := renderBilling(t, types.EmailTypeCreditLowBalance, lowBalanceData(p))
		assertClean(t, subject, html)
		assertText(t, "low balance", html, "Last added $10.00", "Organization Acme", "Checked Oct 4, 2026 at 6:32 PM UTC", "Add credit",
			"you manage billing for the Acme organization on Everstack")
		if depleted {
			assertText(t, "depleted", html, "Prepaid credit used up", "Available credit $0.00", "has no prepaid credit left.", "stop until credit is added")
			if strings.Contains(visibleText(html), "Warning level") {
				t.Error("depleted email shows a warning level")
			}
		} else {
			assertText(t, "warning", html, "Prepaid credit running low", "Available credit $2.00", "Warning level Under 25% of the last top-up",
				"has $2.00 of prepaid credit left, under 25% of the $10.00 last added.")
		}
	}
}

func TestEveryBillingNoticeSendsFromBilling(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, typ := range []types.EmailType{types.EmailTypeCreditLowBalance, types.EmailTypeInstanceOverLimit,
		types.EmailTypeInstanceOverLimitReminder, types.EmailTypeInstancesPaused, types.EmailTypePaymentSuccess} {
		if cfg.SenderFor(typ) != cfg.Senders.Billing {
			t.Errorf("%s is not sent from the billing sender", typ)
		}
	}
}
