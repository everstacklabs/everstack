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

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/service"
)

// Re-export the rich-parameter structs so callers can construct them without
// reaching into the internal/service package directly.
type (
	PaymentSuccessParams = service.PaymentSuccessParams
	PaymentFailedParams  = service.PaymentFailedParams
	UsageWarningParams   = service.UsageWarningParams
)

// Sender is the public interface other services depend on for sending email.
// Add methods here as needed; the underlying implementation lives in internal/service.
type Sender interface {
	SendMagicLink(ctx context.Context, to, magicLink, expiresIn string) error
	SendPasswordReset(ctx context.Context, to, resetURL, expiresIn string) error
	SendEmailVerification(ctx context.Context, to, code, expiresIn string) error
	SendInvitation(ctx context.Context, to, orgName, inviterName, inviterEmail, inviteLink string) error
	SendWelcome(ctx context.Context, to, userName string) error
	SendPaymentSuccess(ctx context.Context, p PaymentSuccessParams) error
	SendPaymentFailed(ctx context.Context, p PaymentFailedParams) error
	SendUsageWarning(ctx context.Context, p UsageWarningParams) error
	SendActivationToken(ctx context.Context, to, activationToken, planTier, gatewayURL string) error
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
//	EVS_SERVICES_EMAIL_LOGO_URL         — overrides default https://everstack.ai/everstack-logo.svg
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
