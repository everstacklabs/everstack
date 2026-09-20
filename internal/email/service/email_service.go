package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/resend"
	"github.com/everstacklabs/everstack/internal/email/templates"
	"github.com/everstacklabs/everstack/internal/email/types"
)

// recipientDomain returns the domain part of an address, for logging. Sends
// are logged by domain rather than by address: it is enough to tell whether
// mail is flowing and which providers are involved, without copying every
// user's address into the log pipeline. Failures log the full address, since
// those are rare, actionable, and you need to know who to retry.
func recipientDomain(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return "unknown"
}

// EmailService handles sending transactional emails
type EmailService struct {
	client *resend.Client
	config *config.Config
}

// NewEmailService creates a new email service
func NewEmailService(cfg *config.Config) *EmailService {
	return &EmailService{
		client: resend.NewClient(cfg.Resend.APIKey),
		config: cfg,
	}
}

// Re-export EmailType from types package for convenience
type EmailType = types.EmailType

const (
	EmailTypeInvitation        = types.EmailTypeInvitation
	EmailTypeWelcome           = types.EmailTypeWelcome
	EmailTypePaymentSuccess    = types.EmailTypePaymentSuccess
	EmailTypePaymentFailed     = types.EmailTypePaymentFailed
	EmailTypeUsageWarning      = types.EmailTypeUsageWarning
	EmailTypeUsageExceeded     = types.EmailTypeUsageExceeded
	EmailTypeActivationToken   = types.EmailTypeActivationToken
	EmailTypeMagicLink         = types.EmailTypeMagicLink
	EmailTypePasswordReset     = types.EmailTypePasswordReset
	EmailTypeEmailVerification = types.EmailTypeEmailVerification
)

// SendEmailRequest is the request to send an email.
// FromOverride/ReplyToOverride let callers override the sender resolved from the type
// (used by SendInvitation to inject the inviter's name and reply-to).
type SendEmailRequest struct {
	Type            EmailType              `json:"type"`
	To              string                 `json:"to"`
	Data            map[string]interface{} `json:"data"`
	FromOverride    string                 `json:"from_override,omitempty"`
	ReplyToOverride string                 `json:"reply_to_override,omitempty"`
}

// SendEmailResponse is the response from sending an email
type SendEmailResponse struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
}

// SendEmail sends a transactional email using the sender configured for its type.
func (s *EmailService) SendEmail(ctx context.Context, req SendEmailRequest) (*SendEmailResponse, error) {
	template, err := templates.GetTemplate(req.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	if req.Data == nil {
		req.Data = make(map[string]interface{})
	}
	if _, ok := req.Data["cloud_url"]; !ok {
		cloudURL := s.config.CloudURL
		if cloudURL == "" {
			cloudURL = "https://app.everstack.ai"
		}
		req.Data["cloud_url"] = cloudURL
	}
	if _, ok := req.Data["docs_url"]; !ok {
		docsURL := s.config.DocsURL
		if docsURL == "" {
			docsURL = "https://docs.everstack.ai"
		}
		req.Data["docs_url"] = docsURL
	}
	if _, ok := req.Data["logo_url"]; !ok {
		logoURL := s.config.LogoURL
		if logoURL == "" {
			logoURL = "https://everstack.ai/everstack-wordmark-dark.png"
		}
		req.Data["logo_url"] = logoURL
	}

	subject, html, err := template.Render(req.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %w", err)
	}

	sender := s.config.SenderFor(req.Type)
	from := sender.FromAddress()
	if req.FromOverride != "" {
		from = req.FromOverride
	}
	replyTo := sender.ReplyTo
	if req.ReplyToOverride != "" {
		replyTo = req.ReplyToOverride
	}

	to := req.To
	tags := []resend.Tag{{Name: "type", Value: string(req.Type)}}
	if s.config.DevRedirectTo != "" {
		// Preserve the original recipient in the subject and as a tag for visibility.
		subject = "[DEV → " + req.To + "] " + subject
		tags = append(tags, resend.Tag{Name: "dev_redirected_from", Value: req.To})
		to = s.config.DevRedirectTo
	}

	// Every send is logged. Before this, a total Resend outage was invisible:
	// the auth layer deliberately collapses send failures into a success
	// response so a transient outage cannot leak whether an account exists,
	// which left no signal anywhere that mail had stopped. Never log req.Data,
	// which carries the magic-link token, reset link, or verification code.
	result, err := s.client.SendEmail(ctx, resend.SendEmailParams{
		From:    from,
		To:      []string{to},
		Subject: subject,
		HTML:    html,
		ReplyTo: replyTo,
		Tags:    tags,
	})
	if err != nil {
		logger.WithError(err).Errorf("email: send failed type=%s to=%s", req.Type, req.To)
		return nil, fmt.Errorf("failed to send email: %w", err)
	}

	logger.Infof("email: sent type=%s domain=%s id=%s", req.Type, recipientDomain(to), result.ID)

	return &SendEmailResponse{
		ID:      result.ID,
		Success: true,
	}, nil
}

// SendMagicLink sends a sign-in magic link to the given email.
// expiresIn is a human-readable string like "10 minutes" or "24 hours".
func (s *EmailService) SendMagicLink(ctx context.Context, to, magicLink, expiresIn string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeMagicLink,
		To:   to,
		Data: map[string]interface{}{
			"magic_link": magicLink,
			"expires_in": expiresIn,
		},
	})
	return err
}

// SendPasswordReset sends a branded password-reset email pointing at the
// hosted /reset-password?token=... page. expiresIn is a human-readable label
// (e.g. "1 hour"). The reset URL is built by the caller so this template
// stays purely presentational.
func (s *EmailService) SendPasswordReset(ctx context.Context, to, resetURL, expiresIn string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypePasswordReset,
		To:   to,
		Data: map[string]interface{}{
			"reset_link": resetURL,
			"expires_in": expiresIn,
		},
	})
	return err
}

// SendEmailVerification sends a branded "verify your email" code. The recipient
// enters the code on the /verify-email page along with the
// pending_authentication_token from the prior auth attempt to complete sign-in.
// expiresIn is a human-readable label (e.g. "10 minutes"). No clickable link
// is included because the verification flow requires the code regardless.
func (s *EmailService) SendEmailVerification(ctx context.Context, to, code, expiresIn string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeEmailVerification,
		To:   to,
		Data: map[string]interface{}{
			"code":       code,
			"expires_in": expiresIn,
		},
	})
	return err
}

// SendInvitation sends an organization invitation email.
// The From header is dynamically constructed as "{Inviter} via Everstack <invites@everstack.ai>"
// and the Reply-To is set to the inviter's actual email so replies go to the right person.
func (s *EmailService) SendInvitation(ctx context.Context, to, orgName, inviterName, inviterEmail, inviteLink string) error {
	inviteSender := s.config.Senders.Invites
	from := inviteSender.FromAddress()
	if inviterName != "" {
		// Linear/Loom-style "{Inviter} (via Everstack) <invites@...>" — keeps the
		// human at the front and signals the platform without the words "via" feeling
		// like part of their name.
		from = inviterName + " (via Everstack) <" + inviteSender.Email + ">"
	}
	replyTo := inviteSender.ReplyTo
	if inviterEmail != "" {
		replyTo = inviterEmail
	}

	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeInvitation,
		To:   to,
		Data: map[string]interface{}{
			"organization_name": orgName,
			"inviter_name":      inviterName,
			"invite_link":       inviteLink,
		},
		FromOverride:    from,
		ReplyToOverride: replyTo,
	})
	return err
}

// SendWelcome sends a welcome email to a new user
func (s *EmailService) SendWelcome(ctx context.Context, to, userName string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeWelcome,
		To:   to,
		Data: map[string]interface{}{
			"user_name": userName,
		},
	})
	return err
}

// PaymentSuccessParams carries optional context for payment receipts.
// All fields except OrgName + Amount are optional — empty values are gracefully
// hidden in the template rather than rendering as blank rows.
type PaymentSuccessParams struct {
	To            string
	OrgName       string
	Amount        float64 // dollars (formatted with 2 decimals)
	PlanTier      string  // e.g. "Pro", "Team" — omitted if empty
	BillingPeriod string  // e.g. "May 1 – May 31, 2026" — omitted if empty
	InvoiceNumber string  // e.g. "INV-2026-0042" — omitted if empty
	InvoiceURL    string  // Stripe-hosted invoice URL — button hidden if empty
}

// SendPaymentSuccess sends a payment confirmation receipt.
func (s *EmailService) SendPaymentSuccess(ctx context.Context, p PaymentSuccessParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypePaymentSuccess,
		To:   p.To,
		Data: map[string]interface{}{
			"organization_name": p.OrgName,
			"amount":            fmt.Sprintf("%.2f", p.Amount),
			"plan_tier":         p.PlanTier,
			"billing_period":    p.BillingPeriod,
			"invoice_number":    p.InvoiceNumber,
			"invoice_url":       p.InvoiceURL,
		},
	})
	return err
}

// PaymentFailedParams carries optional context for payment-failure emails.
type PaymentFailedParams struct {
	To             string
	OrgName        string
	Amount         float64 // dollars; pass 0 to hide the amount in the copy
	Reason         string  // human-readable Stripe decline reason
	NextRetryAt    string  // pre-formatted date/time, e.g. "May 3 at 2:00 PM UTC" — line hidden if empty
	GracePeriodEnd string  // pre-formatted date, e.g. "May 8" — line hidden if empty
}

// SendPaymentFailed sends a payment failure notification.
func (s *EmailService) SendPaymentFailed(ctx context.Context, p PaymentFailedParams) error {
	data := map[string]interface{}{
		"organization_name": p.OrgName,
		"failure_reason":    p.Reason,
		"next_retry_at":     p.NextRetryAt,
		"grace_period_end":  p.GracePeriodEnd,
	}
	if p.Amount > 0 {
		data["amount"] = fmt.Sprintf("%.2f", p.Amount)
	}
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypePaymentFailed,
		To:   p.To,
		Data: data,
	})
	return err
}

// UsageWarningParams carries optional context for usage-warning emails.
type UsageWarningParams struct {
	To           string
	OrgName      string
	UsagePercent float64 // 0–100, also drives the in-email progress bar width
	PlanTier     string  // e.g. "Pro" — falls back to generic copy if empty
	CurrentUsage string  // pre-formatted, e.g. "850k requests" — omitted if empty
	Limit        string  // pre-formatted, e.g. "1M requests" — omitted if empty
	PeriodEnd    string  // pre-formatted, e.g. "May 31" — line hidden if empty
}

// SendUsageWarning sends a usage threshold warning.
func (s *EmailService) SendUsageWarning(ctx context.Context, p UsageWarningParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeUsageWarning,
		To:   p.To,
		Data: map[string]interface{}{
			"organization_name": p.OrgName,
			"usage_percent":     fmt.Sprintf("%.0f", p.UsagePercent),
			"plan_tier":         p.PlanTier,
			"current_usage":     p.CurrentUsage,
			"limit":             p.Limit,
			"period_end":        p.PeriodEnd,
		},
	})
	return err
}

// SendActivationToken sends an activation token email when automatic gateway activation fails
func (s *EmailService) SendActivationToken(ctx context.Context, to, activationToken, planTier, gatewayURL string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: types.EmailTypeActivationToken,
		To:   to,
		Data: map[string]interface{}{
			"activation_token": activationToken,
			"plan_tier":        planTier,
			"gateway_url":      gatewayURL,
		},
	})
	return err
}
