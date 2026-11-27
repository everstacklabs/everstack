package types

// EmailType represents the type of email to send
type EmailType string

const (
	EmailTypeInvitation       EmailType = "invitation"
	EmailTypeWelcome          EmailType = "welcome"
	EmailTypeFounderNote      EmailType = "founder_note"
	EmailTypePaymentSuccess   EmailType = "payment_success"
	EmailTypePaymentFailed    EmailType = "payment_failed"
	EmailTypeUsageWarning     EmailType = "usage_warning"
	EmailTypeUsageExceeded    EmailType = "usage_exceeded"
	EmailTypeCreditLowBalance EmailType = "credit_low_balance"
	// Instance-limit flow (docs/design/instance-over-limit.md): over the
	// plan's instance limit (T0), reminder two days before enforcement, and
	// the enforcement notice naming what was paused.
	EmailTypeInstanceOverLimit         EmailType = "instance_over_limit"
	EmailTypeInstanceOverLimitReminder EmailType = "instance_over_limit_reminder"
	EmailTypeInstancesPaused           EmailType = "instances_paused"
	// Billing lifecycle: plan started (including $0 plans), plan changed,
	// cancellation scheduled or withdrawn, plan ended, credit top-up receipt, and a refund or dispute against credit.
	EmailTypeSubscriptionStarted EmailType = "subscription_started"
	EmailTypeSubscriptionChanged EmailType = "subscription_changed"
	EmailTypeCreditPurchased     EmailType = "credit_purchased"
	EmailTypeCreditReversed      EmailType = "credit_reversed"
	// Auto top-up: a charge went through, or failed and needs action.
	EmailTypeAutoTopUpCharged EmailType = "auto_topup_charged"
	EmailTypeAutoTopUpFailed  EmailType = "auto_topup_failed"
	// Spend limits (docs/design/spend-limits-v2.md 4.5): estimated spend
	// reached 80% or 100% of an organization or API key limit.
	EmailTypeSpendLimit        EmailType = "spend_limit"
	EmailTypeMagicLink         EmailType = "magic_link"
	EmailTypePasswordReset     EmailType = "password_reset"
	EmailTypeEmailVerification EmailType = "email_verification"
	// Saved payment methods (docs/design/payment-methods.md).
	EmailTypeCardAdded          EmailType = "card_added"
	EmailTypeCardRemoved        EmailType = "card_removed"
	EmailTypeCardDefaultChanged EmailType = "card_default_changed"
	EmailTypeCardExpiring       EmailType = "card_expiring"
	// An organization's billing email followed an owner's sign-in email
	// change; sent to the organization's other owners.
	EmailTypeBillingContactChanged EmailType = "billing_contact_changed"
)
