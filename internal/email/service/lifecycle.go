package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Billing lifecycle emails: plan started, plan changed or ended, payment
// method changed, credit purchased, credit reversed. Each Send method builds
// its template data with a pure *Data function so the exact map the provider
// renders is testable without a provider.

// SubscriptionStartedParams describes a plan or license that just started,
// including $0 plans (which get no receipt, so this is their only email).
type SubscriptionStartedParams struct {
	To             string
	IdempotencyKey string
	OrgName        string
	OrgSlug        string
	PlanName       string    // display name, e.g. "Starter"
	Price          string    // plan price as the catalog shows it, e.g. "$0", "$20", "Custom"
	BillingPeriod  string    // "monthly" or "yearly"
	SeatLimit      int       // 0 hides the row
	InstanceLimit  int       // 0 hides the row
	IncludedCredit float64   // USD, self-hosted only; 0 or a Cloud plan hides the row
	CreditOneTime  bool      // the included credit is a one-time starter grant, not per period
	PaymentMethod  string    // e.g. "Visa ending in 4242"; empty hides the row
	PeriodEnd      string    // pre-formatted, e.g. "Nov 2, 2026"; empty hides the row
	SelfHosted     bool      // a self-hosted license: adds the activation pointer (never the token)
	StartedAt      time.Time // when the plan started; zero hides the row
	ActorName      string    // who started it (the member who checked out); empty hides the row
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// planPrice renders a catalog price for its billing period: "$20 / month",
// "$200 / year"; non-dollar prices ("Custom") are shown as is.
func planPrice(price, billingPeriod string) string {
	price = strings.TrimSpace(price)
	if price != "" && strings.HasPrefix(price, "$") {
		if strings.EqualFold(billingPeriod, "yearly") {
			price += " / year"
		} else {
			price += " / month"
		}
	}
	return price
}

// planInterval names a billing period for a detail row.
func planInterval(billingPeriod string) string {
	switch strings.ToLower(strings.TrimSpace(billingPeriod)) {
	case "yearly":
		return "Yearly"
	case "monthly":
		return "Monthly"
	}
	return ""
}

// orgDisplayName is the organization as copy names it: its display name,
// never its slug, and never blank.
func orgDisplayName(name string) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return "your organization"
}

func subscriptionStartedData(p SubscriptionStartedParams) map[string]interface{} {
	price := planPrice(p.Price, p.BillingPeriod)
	data := map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"plan_name":         p.PlanName,
		"interval":          planInterval(p.BillingPeriod),
		"changed_by":        strings.TrimSpace(p.ActorName),
		"changed_at":        FormatNoticeTime(p.StartedAt),
		"price":             price,
		"payment_method":    p.PaymentMethod,
		"period_end":        p.PeriodEnd,
		"self_hosted":       p.SelfHosted,
		"seats":             "",
		"instances":         "",
		"included_credit":   "",
	}
	if p.SeatLimit > 0 {
		data["seats"] = pluralize(p.SeatLimit, "seat", "seats")
	}
	if p.InstanceLimit > 0 {
		data["instances"] = pluralize(p.InstanceLimit, "instance", "instances")
	}
	// Only a self-hosted plan includes credit. A Cloud plan includes its
	// limits, and prepaid credit pays only for usage beyond them, so a Cloud
	// email never states included credit.
	if p.SelfHosted && p.IncludedCredit > 0 {
		suffix := " each billing period"
		if p.CreditOneTime {
			suffix = " one-time starter credit"
		}
		data["included_credit"] = FormatMoney(p.IncludedCredit, "usd") + suffix
	}
	return data
}

// SendSubscriptionStarted confirms a newly started plan or license.
func (s *EmailService) SendSubscriptionStarted(ctx context.Context, p SubscriptionStartedParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeSubscriptionStarted, To: p.To, IdempotencyKey: p.IdempotencyKey, Data: subscriptionStartedData(p),
	})
	return err
}

// SubscriptionChange selects the variant of the plan-change email.
type SubscriptionChange string

const (
	SubscriptionChangePlanChanged     SubscriptionChange = "plan_changed"
	SubscriptionChangeCancelScheduled SubscriptionChange = "cancel_scheduled"
	SubscriptionChangeResumed         SubscriptionChange = "resumed"
	SubscriptionChangeEnded           SubscriptionChange = "ended"
)

// SubscriptionChangedParams describes a change to an existing plan.
type SubscriptionChangedParams struct {
	To               string
	IdempotencyKey   string
	OrgName          string
	OrgSlug          string
	Change           SubscriptionChange
	PlanName         string // the plan now (plan_changed) or the plan affected
	PreviousPlanName string // plan_changed only
	EffectiveDate    string // cancel_scheduled: when access ends; pre-formatted; empty hides it
	BillingPeriod    string // "monthly" or "yearly"; empty hides the interval row
	Price            string // catalog price of PlanName, e.g. "$20"; empty hides the row
	ChangedAt        time.Time
	ActorName        string // the member who made the change; empty hides the row
	FallbackPlan     string // the plan the organization falls back to when this one ends
}

func subscriptionChangedData(p SubscriptionChangedParams) map[string]interface{} {
	fallback := strings.TrimSpace(p.FallbackPlan)
	if fallback == "" {
		fallback = "Starter"
	}
	return map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"plan_name":         p.PlanName,
		"previous_plan":     p.PreviousPlanName,
		"effective_date":    p.EffectiveDate,
		"interval":          planInterval(p.BillingPeriod),
		"price":             planPrice(p.Price, p.BillingPeriod),
		"changed_at":        FormatNoticeTime(p.ChangedAt),
		"changed_by":        strings.TrimSpace(p.ActorName),
		"fallback_plan":     fallback,
		"plan_changed":      p.Change == SubscriptionChangePlanChanged,
		"cancel_scheduled":  p.Change == SubscriptionChangeCancelScheduled,
		"resumed":           p.Change == SubscriptionChangeResumed,
		"ended":             p.Change == SubscriptionChangeEnded,
	}
}

// SendSubscriptionChanged sends the plan changed, cancellation scheduled,
// cancellation withdrawn, or plan ended email.
func (s *EmailService) SendSubscriptionChanged(ctx context.Context, p SubscriptionChangedParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeSubscriptionChanged, To: p.To, IdempotencyKey: p.IdempotencyKey, Data: subscriptionChangedData(p),
	})
	return err
}

// CreditPurchaseParams is a prepaid credit top-up receipt. Credit amounts are
// USD (the wallet's unit); Tax and Total are in the charge Currency.
type CreditPurchaseParams struct {
	To             string
	IdempotencyKey string
	OrgName        string
	OrgSlug        string
	CreditAdded    float64
	Tax            float64 // 0 hides the row
	Total          float64
	Currency       string
	Balance        float64   // spendable credit after the top-up
	BalanceKnown   bool      // false hides the balance row
	InvoiceURL     string    // Stripe-hosted invoice; empty hides the button
	PaidAt         time.Time // zero hides the row
	ActorName      string    // the member who bought it; empty hides the row
	Approved       bool      // held for review, then approved: says so
}

// moneyWithCode is FormatMoney with the ISO code spelled out, for the total
// of a receipt: "$27.13 USD", "1000 JPY".
func moneyWithCode(amount float64, currency string) string {
	s := FormatMoney(amount, currency)
	code := normalizeCurrency(currency)
	if strings.HasSuffix(s, " "+code) {
		return s
	}
	return s + " " + code
}

func creditPurchaseData(p CreditPurchaseParams) map[string]interface{} {
	data := map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"credit_added":      FormatMoney(p.CreditAdded, "usd"),
		"tax":               "",
		"total":             moneyWithCode(p.Total, p.Currency),
		"balance":           "",
		"invoice_url":       p.InvoiceURL,
		"changed_by":        strings.TrimSpace(p.ActorName),
		"changed_at":        FormatNoticeTime(p.PaidAt),
		"approved":          p.Approved,
	}
	if p.Tax > 0 {
		data["tax"] = FormatMoney(p.Tax, p.Currency)
	}
	if p.BalanceKnown {
		data["balance"] = FormatMoney(p.Balance, "usd")
	}
	return data
}

// SendCreditPurchased sends the receipt for a credit top-up.
func (s *EmailService) SendCreditPurchased(ctx context.Context, p CreditPurchaseParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeCreditPurchased, To: p.To, IdempotencyKey: p.IdempotencyKey, Data: creditPurchaseData(p),
	})
	return err
}

// CreditReversalKind selects the variant of the credit reversal email.
type CreditReversalKind string

const (
	CreditReversalRefund        CreditReversalKind = "refund"
	CreditReversalDisputeOpened CreditReversalKind = "dispute_opened"
	CreditReversalDisputeWon    CreditReversalKind = "dispute_won"
	CreditReversalDisputeLost   CreditReversalKind = "dispute_lost"
	// CreditReversalRefundFailed: a refund that had already removed credit
	// failed or was canceled, so the money stayed with us and the credit
	// was put back.
	CreditReversalRefundFailed CreditReversalKind = "refund_failed"
)

// CreditReversalParams describes a refund or dispute against a top-up and
// what it did to the organization's credit.
type CreditReversalParams struct {
	To             string
	IdempotencyKey string
	OrgName        string
	OrgSlug        string
	Kind           CreditReversalKind
	Amount         float64 // refunded or disputed charge amount, in Currency
	Currency       string
	Credit         float64   // USD credit removed, held, released, or restored
	Balance        float64   // spendable credit after the change
	BalanceKnown   bool      // false hides the balance row
	At             time.Time // when the credit moved; zero hides the row
}

// creditReversalLabels names the detail rows of each reversal: what the
// amount is, what happened to the credit, who acted and when.
func creditReversalLabels(kind CreditReversalKind) (amount, credit, byLabel, by, at string) {
	switch kind {
	case CreditReversalRefund:
		return "Refunded", "Credit removed", "Issued by", "Everstack", "Issued"
	case CreditReversalDisputeOpened:
		return "Disputed amount", "Credit on hold", "Opened by", "Your bank", "Opened"
	case CreditReversalDisputeWon:
		return "Disputed amount", "Credit released", "Decided by", "Your bank", "Resolved"
	case CreditReversalRefundFailed:
		return "Refund amount", "Credit restored", "", "", "Failed"
	default: // dispute lost
		return "Disputed amount", "Credit removed", "Decided by", "Your bank", "Closed"
	}
}

func creditReversalData(p CreditReversalParams) map[string]interface{} {
	amountLabel, creditLabel, byLabel, by, atLabel := creditReversalLabels(p.Kind)
	balance := ""
	if p.BalanceKnown {
		balance = FormatMoney(p.Balance, "usd")
	}
	return map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"amount":            moneyWithCode(p.Amount, p.Currency),
		"credit":            FormatMoney(p.Credit, "usd"),
		"balance":           balance,
		"amount_label":      amountLabel,
		"credit_label":      creditLabel,
		"by_label":          byLabel,
		"changed_by":        by,
		"at_label":          atLabel,
		"changed_at":        FormatNoticeTime(p.At),
		"refund":            p.Kind == CreditReversalRefund,
		"dispute_opened":    p.Kind == CreditReversalDisputeOpened,
		"dispute_won":       p.Kind == CreditReversalDisputeWon,
		"dispute_lost":      p.Kind == CreditReversalDisputeLost,
		"refund_failed":     p.Kind == CreditReversalRefundFailed,
	}
}

// SendCreditReversed tells billing managers that a refund or dispute changed
// the organization's credit.
func (s *EmailService) SendCreditReversed(ctx context.Context, p CreditReversalParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeCreditReversed, To: p.To, IdempotencyKey: p.IdempotencyKey, Data: creditReversalData(p),
	})
	return err
}
