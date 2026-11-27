package service

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/templates"
	"github.com/everstacklabs/everstack/internal/email/types"
)

// Preview is one example of an email variant, built through the same data
// builder its Send method uses, so a preview shows exactly what a customer
// gets. Used by the render tests and services/email/cmd/preview.
type Preview struct {
	Name string // file-safe, e.g. "plan-changed"
	Type types.EmailType
	Data map[string]interface{}
	// From overrides the sender (an invitation is sent as the inviter).
	From string
	// Note marks a template kept as is (dead, or owned by another change).
	Note string
}

// RenderedPreview is a Preview rendered as SendEmail renders it.
type RenderedPreview struct {
	Preview
	Subject, HTML, From, ReplyTo string
}

// RenderPreview fills the same defaults SendEmail does, renders the template
// and resolves the sender.
func RenderPreview(cfg *config.Config, p Preview) (RenderedPreview, error) {
	tpl, err := templates.GetTemplate(p.Type)
	if err != nil {
		return RenderedPreview{}, err
	}
	data := make(map[string]interface{}, len(p.Data)+6)
	for k, v := range p.Data {
		data[k] = v
	}
	fillDefaults(cfg, data)
	subject, html, err := tpl.Render(data)
	if err != nil {
		return RenderedPreview{}, fmt.Errorf("%s: %w", p.Name, err)
	}
	sender := cfg.SenderFor(p.Type)
	from := sender.FromAddress()
	if p.From != "" {
		from = p.From
	}
	return RenderedPreview{Preview: p, Subject: subject, HTML: html, From: from, ReplyTo: sender.ReplyTo}, nil
}

// optionalPreviews holds preview builders that register themselves from
// their own file with registerPreviews, so no other file names them and the
// file can be left out of a build. They are filled by package-level variable
// initialisers, which all run before Previews can be called.
var optionalPreviews []func(add func(name string, typ types.EmailType, data map[string]interface{}))

func registerPreviews(build func(add func(name string, typ types.EmailType, data map[string]interface{}))) bool {
	optionalPreviews = append(optionalPreviews, build)
	return true
}

// Previews returns every variant of every email, with sample data.
func Previews() []Preview {
	at := time.Date(2026, 10, 4, 18, 32, 0, 0, time.UTC)
	const org, slug = "Acme", "acme"
	var out []Preview
	add := func(name string, typ types.EmailType, data map[string]interface{}) {
		out = append(out, Preview{Name: name, Type: typ, Data: data})
	}

	// Account.
	add("magic-link", types.EmailTypeMagicLink, magicLinkData("ada@example.com", "https://app.everstack.ai/verify-magic-link#token=sample", "24 hours", at))
	add("password-reset", types.EmailTypePasswordReset, passwordResetData("ada@example.com", "https://app.everstack.ai/reset-password?token=sample", "1 hour", at))
	add("email-verification", types.EmailTypeEmailVerification, verificationData("ada@example.com", "493012", "10 minutes", at))
	out = append(out, Preview{Name: "invitation", Type: types.EmailTypeInvitation,
		Data: invitationData("new@example.com", org, "Jane Smith", "jane@acme.com", "https://app.everstack.ai/invite/sample", "member", at.Add(7*24*time.Hour)),
		From: "Jane Smith (via Everstack) <invites@everstack.ai>"})
	add("invitation-self-hosted", types.EmailTypeInvitation, invitationData("new@example.com", "", "", "jane@acme.com", "https://gateway.acme.com/auth/invite?token=sample", "admin", at.Add(7*24*time.Hour)))
	add("welcome", types.EmailTypeWelcome, welcomeData("sam@example.com", "Sam Riley"))

	bookingWelcome := welcomeData("sam@example.com", "Sam Riley")
	bookingWelcome["booking_url"] = "https://everstack.ai/book"
	add("welcome-booking", types.EmailTypeWelcome, bookingWelcome)

	// Previews kept in their own files (see optionalPreviews), listed here
	// so they keep their place after the account emails.
	for _, extra := range optionalPreviews {
		extra(add)
	}

	// Plan lifecycle.
	add("plan-started-cloud", types.EmailTypeSubscriptionStarted, subscriptionStartedData(SubscriptionStartedParams{
		OrgName: org, OrgSlug: slug, PlanName: "Build", Price: "$20", BillingPeriod: "monthly", SeatLimit: 3, InstanceLimit: 3,
		PaymentMethod: "Visa ending in 4242", PeriodEnd: "Nov 4, 2026", StartedAt: at, ActorName: "Olive Owner"}))
	add("plan-started-yearly", types.EmailTypeSubscriptionStarted, subscriptionStartedData(SubscriptionStartedParams{
		OrgName: org, OrgSlug: slug, PlanName: "Build", Price: "$200", BillingPeriod: "yearly", SeatLimit: 3, InstanceLimit: 3,
		PaymentMethod: "Visa ending in 4242", PeriodEnd: "Oct 4, 2027", StartedAt: at, ActorName: "Olive Owner"}))
	add("plan-started-self-hosted", types.EmailTypeSubscriptionStarted, subscriptionStartedData(SubscriptionStartedParams{
		OrgName: org, OrgSlug: slug, PlanName: "Scale", Price: "$1000", BillingPeriod: "yearly", SeatLimit: 10, InstanceLimit: 10,
		IncludedCredit: 50, SelfHosted: true, StartedAt: at}))
	for _, c := range []struct {
		name   string
		change SubscriptionChange
		actor  string
	}{
		{"plan-changed", SubscriptionChangePlanChanged, "Olive Owner"},
		{"plan-changed-in-stripe", SubscriptionChangePlanChanged, ""},
		{"plan-ending", SubscriptionChangeCancelScheduled, "Olive Owner"},
		{"plan-renewing", SubscriptionChangeResumed, "Olive Owner"},
		{"plan-ended", SubscriptionChangeEnded, ""},
	} {
		add(c.name, types.EmailTypeSubscriptionChanged, subscriptionChangedData(SubscriptionChangedParams{
			OrgName: org, OrgSlug: slug, Change: c.change, PlanName: "Scale", PreviousPlanName: "Build",
			EffectiveDate: "Nov 4, 2026", BillingPeriod: "monthly", Price: "$100", ChangedAt: at, ActorName: c.actor, FallbackPlan: "Starter"}))
	}

	// Credit.
	add("credit-purchased", types.EmailTypeCreditPurchased, creditPurchaseData(CreditPurchaseParams{
		OrgName: org, OrgSlug: slug, CreditAdded: 25, Tax: 2.13, Total: 27.13, Currency: "usd", Balance: 31.5, BalanceKnown: true,
		InvoiceURL: "https://invoice.stripe.com/i/sample", PaidAt: at, ActorName: "Olive Owner"}))
	add("credit-purchased-approved", types.EmailTypeCreditPurchased, creditPurchaseData(CreditPurchaseParams{
		OrgName: org, OrgSlug: slug, CreditAdded: 500, Total: 500, Currency: "usd", Balance: 512.4, BalanceKnown: true,
		PaidAt: at, ActorName: "Olive Owner", Approved: true}))
	for _, k := range []CreditReversalKind{CreditReversalRefund, CreditReversalDisputeOpened, CreditReversalDisputeWon, CreditReversalDisputeLost, CreditReversalRefundFailed} {
		add("credit-"+string(k), types.EmailTypeCreditReversed, creditReversalData(CreditReversalParams{
			OrgName: org, OrgSlug: slug, Kind: k, Amount: 27.13, Currency: "usd", Credit: 25, Balance: 6.5, BalanceKnown: true, At: at}))
	}

	// Auto top-up.
	add("auto-topup-charged", types.EmailTypeAutoTopUpCharged, autoTopUpData(AutoTopUpParams{
		OrgName: org, OrgSlug: slug, Outcome: AutoTopUpCharged, Amount: 25, CardLabel: "Visa ending in 4242",
		Balance: 31.5, BalanceKnown: true, RemainingCap: 50, MonthlyCap: 75, RemainingCapKnown: true, At: at}))
	add("auto-topup-failed", types.EmailTypeAutoTopUpFailed, autoTopUpData(AutoTopUpParams{
		OrgName: org, OrgSlug: slug, Outcome: AutoTopUpFailed, Amount: 25, CardLabel: "Visa ending in 4242",
		Reason: "Your card was declined. Update the card or set a different default card.", NextAttempt: at.Add(time.Hour), At: at}))
	add("auto-topup-paused", types.EmailTypeAutoTopUpFailed, autoTopUpData(AutoTopUpParams{
		OrgName: org, OrgSlug: slug, Outcome: AutoTopUpFailed, Amount: 25, CardLabel: "Visa ending in 4242",
		Reason: "Your card was declined. Update the card or set a different default card.", Paused: true, At: at}))
	add("auto-topup-paused-dispute", types.EmailTypeAutoTopUpFailed, autoTopUpData(AutoTopUpParams{
		OrgName: org, OrgSlug: slug, Outcome: AutoTopUpFailed, Paused: true, PauseReason: "dispute",
		Reason: "A credit payment was disputed with your bank, so we will not charge your card again until an owner or admin turns auto top-up back on.", At: at}))
	add("auto-topup-no-card", types.EmailTypeAutoTopUpFailed, autoTopUpData(AutoTopUpParams{
		OrgName: org, OrgSlug: slug, Outcome: AutoTopUpFailed, Amount: 25, NoCard: true,
		Reason: "There is no default card to charge. Add a card in Billing.", NextAttempt: at.Add(time.Hour), At: at}))

	// Spend limits: an organization limit and an API key limit, at 80% and
	// 100%, blocking (enforced and not yet enforced) and alert only.
	resets := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		p    SpendLimitParams
	}{
		{"spend-limit-80-org", SpendLimitParams{Period: "monthly", Percent: 80, Spent: 40.12, Limit: 50, Block: true}},
		{"spend-limit-100-org-shadow", SpendLimitParams{Period: "monthly", Percent: 100, Spent: 50.4, Limit: 50, Block: true}},
		{"spend-limit-100-org-enforced", SpendLimitParams{Period: "monthly", Percent: 100, Spent: 50.4, Limit: 50, Block: true, Enforced: true}},
		{"spend-limit-80-key-alert", SpendLimitParams{APIKeyName: "Production backend", Period: "daily", Percent: 80, Spent: 8.1, Limit: 10}},
		{"spend-limit-100-key-creator", SpendLimitParams{APIKeyName: "Production backend", Period: "daily", Percent: 100, Spent: 10.02, Limit: 10, Block: true, Creator: true}},
	} {
		c.p.OrgName, c.p.OrgSlug, c.p.ResetsAt, c.p.CheckedAt = org, slug, resets, at
		add(c.name, types.EmailTypeSpendLimit, spendLimitData(c.p))
	}

	// Receipt and credit balance.
	add("payment-receipt", types.EmailTypePaymentSuccess, paymentSuccessData(PaymentSuccessParams{
		OrgName: org, OrgSlug: slug, Amount: 100, Currency: "usd", PlanTier: "Scale", BillingPeriod: "Oct 4 to Nov 4, 2026",
		InvoiceNumber: "INV-2026-0042", InvoiceURL: "https://invoice.stripe.com/i/sample", PaidAt: at}))
	add("credit-low", types.EmailTypeCreditLowBalance, lowBalanceData(LowBalanceParams{
		OrgName: org, OrgSlug: slug, Available: 2.4, Funded: 25, ThresholdPercent: 10, Currency: "usd", CheckedAt: at}))
	add("credit-used-up", types.EmailTypeCreditLowBalance, lowBalanceData(LowBalanceParams{
		OrgName: org, OrgSlug: slug, Available: 0, Funded: 25, Currency: "usd", Depleted: true, CheckedAt: at}))

	// Instance limit.
	for _, st := range []struct {
		name  string
		typ   types.EmailType
		stage InstanceLimitStage
	}{
		{"instance-over-limit", types.EmailTypeInstanceOverLimit, InstanceLimitStageOverLimit},
		{"instance-over-limit-reminder", types.EmailTypeInstanceOverLimitReminder, InstanceLimitStageReminder},
		{"instances-paused", types.EmailTypeInstancesPaused, InstanceLimitStagePaused},
	} {
		add(st.name, st.typ, instanceLimitData(InstanceLimitParams{
			OrgName: org, OrgSlug: slug, Stage: st.stage, InstanceLimit: 3, SlotsUsed: 5, GraceEndsAt: at.Add(7 * 24 * time.Hour),
			KeptSlugs: []string{"prod", "staging", "eval"}, PausedSlugs: []string{"dev", "scratch"}, ExtraInstancePriceUSD: 5, NoticedAt: at,
		}, "https://app.everstack.ai/acme/instances"))
	}

	// Saved payment methods.
	for _, ev := range []CardNoticeEvent{CardNoticeAdded, CardNoticeRemoved, CardNoticeDefaultChanged, CardNoticeExpiring} {
		for _, k := range []struct {
			name string
			id   PaymentMethodIdentity
		}{
			{"card", PaymentMethodIdentity{Type: "card", Brand: "visa", Last4: "4242"}},
			{"link", PaymentMethodIdentity{Type: "link", LinkEmail: "ada@example.com"}},
			{"wallet", PaymentMethodIdentity{Type: "card", Brand: "visa", Last4: "4242", Wallet: "apple_pay"}},
		} {
			if ev == CardNoticeExpiring && k.id.Type == "link" {
				continue // only a card expires; Link is never sent this email
			}
			label, isCard := PaymentMethodLabel(k.id)
			p := CardNoticeParams{OrgName: org, OrgSlug: slug, Event: ev, Type: k.id.Type, Brand: k.id.Brand, Last4: k.id.Last4,
				MethodLabel: label, IsCard: isCard, ChangedAt: at}
			if k.id.Last4 != "" {
				p.ExpMonth, p.ExpYear = 8, 2029
			}
			switch ev {
			case CardNoticeAdded:
				p.IsDefault = true
			case CardNoticeRemoved:
				p.Source, p.ActorName = CardNoticeSourceMember, "Olive Owner"
				p.RemainingKnown, p.RemainingCount, p.RemainingDefault = true, 1, "Mastercard ending in 4444"
			case CardNoticeDefaultChanged:
				p.Source, p.ActorName, p.PreviousDefault = CardNoticeSourceMember, "Olive Owner", "Mastercard ending in 4444"
			case CardNoticeExpiring:
				p.ChangedAt = time.Time{}
			}
			add("card-"+string(ev)+"-"+k.name, cardNoticeTypes[ev], cardNoticeData(p))
		}
	}
	removed := func(src CardNoticeSource) map[string]interface{} {
		label, isCard := PaymentMethodLabel(PaymentMethodIdentity{Type: "card", Brand: "visa", Last4: "4242"})
		return cardNoticeData(CardNoticeParams{OrgName: org, OrgSlug: slug, Event: CardNoticeRemoved, Type: "card", Brand: "visa", Last4: "4242",
			MethodLabel: label, IsCard: isCard, ChangedAt: at, Source: src, RemainingKnown: true})
	}
	add("card-removed-in-stripe", types.EmailTypeCardRemoved, removed(CardNoticeSourceStripe))
	add("card-removed-automatically", types.EmailTypeCardRemoved, removed(CardNoticeSourceSync))

	// Kept as is: payment failed waits for the dunning change (PR #821).
	out = append(out, Preview{Name: "payment-failed", Type: types.EmailTypePaymentFailed, Note: "unchanged, waits for #821",
		Data: map[string]interface{}{
			"organization_name": org, "org_slug": slug, "amount": "$100.00",
			"failure_reason": "Your card was declined.", "next_retry_at": "Oct 7 at 6:32 PM UTC", "grace_period_end": "Oct 11, 2026",
		}})
	return out
}
