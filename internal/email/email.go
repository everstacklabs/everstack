// Package email is the entry point to outbound transactional email.
// Callers construct a Sender with NewSenderFromEnv; the subpackages holding
// the provider client, templates, and rendering stay unexported.
//
// This lives under internal/ rather than services/ so the gateway binary can
// reach it. The gateway hosts the builtin self-hosted auth server, which needs
// to send magic-link and invitation mail, and cmd/ cannot import services/.
// services/email remains as a thin alias so existing cloud callers are
// unaffected.
package email

import (
	"context"
	"os"
	"time"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/service"
)

// Re-export the rich-parameter structs so callers can construct them without
// reaching into the internal/service package directly.
type (
	PaymentSuccessParams = service.PaymentSuccessParams
	PaymentFailedParams  = service.PaymentFailedParams
	UsageWarningParams   = service.UsageWarningParams
	LowBalanceParams     = service.LowBalanceParams
	InstanceLimitParams  = service.InstanceLimitParams
	InstanceLimitStage   = service.InstanceLimitStage

	SubscriptionStartedParams = service.SubscriptionStartedParams
	SubscriptionChangedParams = service.SubscriptionChangedParams
	SubscriptionChange        = service.SubscriptionChange
	CreditPurchaseParams      = service.CreditPurchaseParams
	CreditReversalParams      = service.CreditReversalParams
	CreditReversalKind        = service.CreditReversalKind
	AutoTopUpParams           = service.AutoTopUpParams
	SpendLimitParams          = service.SpendLimitParams
	AutoTopUpOutcome          = service.AutoTopUpOutcome
)

// Auto top-up notice variants (see service.AutoTopUpOutcome).
const (
	AutoTopUpCharged = service.AutoTopUpCharged
	AutoTopUpFailed  = service.AutoTopUpFailed
)

// Subscription change variants (see service.SubscriptionChange).
const (
	SubscriptionChangePlanChanged     = service.SubscriptionChangePlanChanged
	SubscriptionChangeCancelScheduled = service.SubscriptionChangeCancelScheduled
	SubscriptionChangeResumed         = service.SubscriptionChangeResumed
	SubscriptionChangeEnded           = service.SubscriptionChangeEnded
)

// Credit reversal variants (see service.CreditReversalKind).
const (
	CreditReversalRefund        = service.CreditReversalRefund
	CreditReversalDisputeOpened = service.CreditReversalDisputeOpened
	CreditReversalDisputeWon    = service.CreditReversalDisputeWon
	CreditReversalDisputeLost   = service.CreditReversalDisputeLost
)

// BillingContactChangedParams: see service.BillingContactChangedParams.
type BillingContactChangedParams = service.BillingContactChangedParams

// Saved payment method notice parameters (see service.CardNoticeParams).
type (
	CardNoticeParams = service.CardNoticeParams
	CardNoticeEvent  = service.CardNoticeEvent
)

// Saved payment method notices (see service.CardNoticeEvent).
const (
	CardNoticeAdded          = service.CardNoticeAdded
	CardNoticeRemoved        = service.CardNoticeRemoved
	CardNoticeDefaultChanged = service.CardNoticeDefaultChanged
	CardNoticeExpiring       = service.CardNoticeExpiring
)

// Where a payment method change came from (see service.CardNoticeSource).
type CardNoticeSource = service.CardNoticeSource

const (
	CardNoticeSourceMember = service.CardNoticeSourceMember
	CardNoticeSourceStripe = service.CardNoticeSourceStripe
	CardNoticeSourceSync   = service.CardNoticeSourceSync
)

// PaymentMethodIdentity is what names a saved payment method in copy.
type PaymentMethodIdentity = service.PaymentMethodIdentity

// PaymentMethodLabel names a payment method for customer copy ("Visa ending
// in 4242", "Link (a***@gmail.com)", "Apple Pay (Visa ending in 4242)").
var PaymentMethodLabel = service.PaymentMethodLabel

// Instance-limit email stages (see service.InstanceLimitStage).
const (
	InstanceLimitStageOverLimit = service.InstanceLimitStageOverLimit
	InstanceLimitStageReminder  = service.InstanceLimitStageReminder
	InstanceLimitStagePaused    = service.InstanceLimitStagePaused
)

// MajorUnits converts a provider minor-unit amount to major units at the
// currency's own precision (see service.MajorUnits). Re-exported so billing
// code converts Stripe amounts the same way the emails format them.
func MajorUnits(minor int64, currency string) float64 { return service.MajorUnits(minor, currency) }

// Sender is the public interface other services depend on for sending email.
// Add methods here as needed; the underlying implementation lives in internal/service.
type Sender interface {
	SendMagicLink(ctx context.Context, to, magicLink, expiresIn string) error
	SendPasswordReset(ctx context.Context, to, resetURL, expiresIn string) error
	SendEmailVerification(ctx context.Context, to, code, expiresIn string) error
	SendInvitation(ctx context.Context, to, orgName, inviterName, inviterEmail, inviteLink, role string, expiresAt time.Time) error
	SendWelcome(ctx context.Context, to, userName string) error
	// SendCloudWelcome is the Everstack Cloud welcome, which also states the
	// signup credit and its 30-day expiry.
	SendCloudWelcome(ctx context.Context, to, userName string, starterCreditUSD float64) error
	SendCloudWelcomeOnce(ctx context.Context, to, userName string, starterCreditUSD float64, idempotencyKey string) error
	SendPaymentSuccess(ctx context.Context, p PaymentSuccessParams) error
	SendPaymentFailed(ctx context.Context, p PaymentFailedParams) error
	SendUsageWarning(ctx context.Context, p UsageWarningParams) error
	SendLowBalance(ctx context.Context, p LowBalanceParams) error
	SendInstanceLimit(ctx context.Context, p InstanceLimitParams) error
	SendSubscriptionStarted(ctx context.Context, p SubscriptionStartedParams) error
	SendSubscriptionChanged(ctx context.Context, p SubscriptionChangedParams) error
	SendCreditPurchased(ctx context.Context, p CreditPurchaseParams) error
	SendCreditReversed(ctx context.Context, p CreditReversalParams) error
	SendAutoTopUp(ctx context.Context, p AutoTopUpParams) error
	SendSpendLimit(ctx context.Context, p SpendLimitParams) error
	SendCardNotice(ctx context.Context, p CardNoticeParams) error
	SendBillingContactChanged(ctx context.Context, p BillingContactChangedParams) error
}

// NewSenderFromEnv builds a Sender from EVS_* environment variables.
// Returns nil if EVS_SERVICES_EMAIL_RESEND_API_KEY is unset, signaling "no
// outbound email configured" — callers should treat email as best-effort and
// degrade gracefully (e.g., log magic-link tokens instead of mailing them).
//
// Recognized env vars (all optional except the API key):
//
//	EVS_SERVICES_EMAIL_RESEND_API_KEY   — Resend API key (required)
//	EVS_SERVICES_EMAIL_CLOUD_URL        — overrides default https://app.everstack.ai
//	EVS_SERVICES_EMAIL_DOCS_URL         — overrides default https://docs.everstack.ai
//	EVS_SERVICES_EMAIL_BOOKING_URL:     optional https booking link for founder mail
//	EVS_SERVICES_EMAIL_UNSUBSCRIBE_SECRET: signing secret, at least 32 characters
//	EVS_SERVICES_EMAIL_UNSUBSCRIBE_SECRET_PREVIOUS: previous secret, still accepted on verify
//	EVS_SERVICES_EMAIL_UNSUBSCRIBE_URL: public unsubscribe route for this environment
//	EVS_SERVICES_EMAIL_LOGO_URL:        overrides default https://everstack.ai/everstack-wordmark-dark-email.png
//	EVS_SERVICES_EMAIL_LOGO_DARK_URL:   light logo shown in dark mode; defaults to the
//	                                    light wordmark only while LOGO_URL is the default
//	EVS_SERVICES_EMAIL_FROM             — overrides the From on every message,
//	                                      as "Name <address@example.com>" or a
//	                                      bare address. Required to send from
//	                                      your own domain.
//	EVS_SERVICES_EMAIL_REPLY_TO         — overrides Reply-To on every message
//	EVS_SERVICES_EMAIL_DEV_REDIRECT_TO  — when set (e.g. "delivered@resend.dev"),
//	                                      ALL outbound mail is redirected to this
//	                                      address with the original recipient
//	                                      preserved in the subject. Use in dev
//	                                      to exercise the live Resend path
//	                                      without burning quota or mailing users.
func NewSenderFromEnv() Sender {
	apiKey := os.Getenv("EVS_SERVICES_EMAIL_RESEND_API_KEY")
	if apiKey == "" {
		return nil
	}
	cfg := config.DefaultConfig()
	cfg.ApplyOutreachEnv()
	cfg.Resend.APIKey = apiKey
	if v := os.Getenv("EVS_SERVICES_EMAIL_CLOUD_URL"); v != "" {
		cfg.CloudURL = v
	}
	if v := os.Getenv("EVS_SERVICES_EMAIL_DOCS_URL"); v != "" {
		cfg.DocsURL = v
	}
	if v := os.Getenv("EVS_SERVICES_EMAIL_LOGO_URL"); v != "" {
		cfg.LogoURL = v
	}
	if v := os.Getenv("EVS_SERVICES_EMAIL_LOGO_DARK_URL"); v != "" {
		cfg.LogoDarkURL = v
	}
	if v := os.Getenv("EVS_SERVICES_EMAIL_DEV_REDIRECT_TO"); v != "" {
		cfg.DevRedirectTo = v
	}
	// Sender identity overrides. The built-in defaults are everstack.ai
	// addresses, which only the hosted product can send as: a provider will
	// refuse mail From a domain the account has not verified. Anyone running
	// their own instance needs to substitute their own, so these are the one
	// piece of the config that has to be reachable without editing code.
	cfg.ApplySenderOverrides(
		os.Getenv("EVS_SERVICES_EMAIL_FROM"),
		os.Getenv("EVS_SERVICES_EMAIL_REPLY_TO"),
	)
	return service.NewEmailService(cfg)
}

// CreditReversalRefundFailed is the "refund failed, credit restored" variant
// of the credit reversal email.
const CreditReversalRefundFailed = service.CreditReversalRefundFailed
