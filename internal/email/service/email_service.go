package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/email/config"
	"github.com/everstacklabs/everstack/internal/email/resend"
	"github.com/everstacklabs/everstack/internal/email/templates"
	"github.com/everstacklabs/everstack/internal/email/types"
	"github.com/everstacklabs/everstack/internal/lib/logger"
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
	EmailTypeInvitation                = types.EmailTypeInvitation
	EmailTypeWelcome                   = types.EmailTypeWelcome
	EmailTypePaymentSuccess            = types.EmailTypePaymentSuccess
	EmailTypePaymentFailed             = types.EmailTypePaymentFailed
	EmailTypeUsageWarning              = types.EmailTypeUsageWarning
	EmailTypeUsageExceeded             = types.EmailTypeUsageExceeded
	EmailTypeCreditLowBalance          = types.EmailTypeCreditLowBalance
	EmailTypeInstanceOverLimit         = types.EmailTypeInstanceOverLimit
	EmailTypeInstanceOverLimitReminder = types.EmailTypeInstanceOverLimitReminder
	EmailTypeInstancesPaused           = types.EmailTypeInstancesPaused
	EmailTypeMagicLink                 = types.EmailTypeMagicLink
	EmailTypePasswordReset             = types.EmailTypePasswordReset
	EmailTypeEmailVerification         = types.EmailTypeEmailVerification
	EmailTypeSubscriptionStarted       = types.EmailTypeSubscriptionStarted
	EmailTypeSubscriptionChanged       = types.EmailTypeSubscriptionChanged
	EmailTypeCreditPurchased           = types.EmailTypeCreditPurchased
	EmailTypeCreditReversed            = types.EmailTypeCreditReversed
	EmailTypeAutoTopUpCharged          = types.EmailTypeAutoTopUpCharged
	EmailTypeAutoTopUpFailed           = types.EmailTypeAutoTopUpFailed
	EmailTypeSpendLimit                = types.EmailTypeSpendLimit
	EmailTypeBillingContactChanged     = types.EmailTypeBillingContactChanged
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
	// IdempotencyKey is forwarded to the provider so a retried send (lost
	// response, timeout) is delivered once. Optional.
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
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
	fillDefaults(s.config, req.Data)

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

		IdempotencyKey: req.IdempotencyKey,
		Headers:        req.Headers,
	})
	if err != nil {
		logger.Errorf("%s", emailSendFailureLog(req.Type, to, err))
		return nil, fmt.Errorf("failed to send email: %w", err)
	}

	logger.Infof("email: sent type=%s domain=%s id=%s", req.Type, recipientDomain(to), result.ID)

	return &SendEmailResponse{
		ID:      result.ID,
		Success: true,
	}, nil
}

// authData is the data every sign-in, reset and verification email shares:
// the account it is about, when it was asked for, and when it expires.
func authData(to, expiresIn string, now time.Time) map[string]interface{} {
	return map[string]interface{}{
		"account_email": strings.TrimSpace(to),
		"requested_at":  FormatNoticeTime(now),
		"expires_in":    strings.TrimSpace(expiresIn),
	}
}

func magicLinkData(to, magicLink, expiresIn string, now time.Time) map[string]interface{} {
	data := authData(to, expiresIn, now)
	data["magic_link"] = magicLink
	return data
}

func passwordResetData(to, resetURL, expiresIn string, now time.Time) map[string]interface{} {
	data := authData(to, expiresIn, now)
	data["reset_link"] = resetURL
	return data
}

func verificationData(to, code, expiresIn string, now time.Time) map[string]interface{} {
	data := authData(to, expiresIn, now)
	data["code"] = code
	return data
}

// fillDefaults adds the links and logos every template uses, unless the
// caller set them: the cloud app, the org billing page, the docs and the
// logo pair.
func fillDefaults(cfg *config.Config, data map[string]interface{}) {
	if _, ok := data["booking_url"]; !ok {
		data["booking_url"] = cfg.BookingLink()
	}
	if _, ok := data["cloud_url"]; !ok {
		cloudURL := cfg.CloudURL
		if cloudURL == "" {
			cloudURL = "https://app.everstack.ai"
		}
		data["cloud_url"] = cloudURL
	}
	if _, ok := data["billing_url"]; !ok {
		// The cloud app scopes billing under the org slug
		// (/{orgSlug}/settings/billing); without a slug the best we can do is
		// land the reader on the app, which routes them to their org.
		base, _ := data["cloud_url"].(string)
		slug, _ := data["org_slug"].(string)
		data["billing_url"] = BillingURL(base, slug)
	}
	if _, ok := data["docs_url"]; !ok {
		docsURL := cfg.DocsURL
		if docsURL == "" {
			docsURL = "https://docs.everstack.ai"
		}
		data["docs_url"] = docsURL
	}
	if _, ok := data["logo_url"]; !ok {
		logoURL := cfg.LogoURL
		if logoURL == "" {
			logoURL = config.DefaultLogoURL
		}
		data["logo_url"] = logoURL
	}
	if _, ok := data["logo_dark_url"]; !ok {
		data["logo_dark_url"] = logoDarkURL(cfg, data["logo_url"])
	}
}

// SendMagicLink sends a sign-in magic link to the given email.
// expiresIn is a human-readable string like "10 minutes" or "24 hours".
func (s *EmailService) SendMagicLink(ctx context.Context, to, magicLink, expiresIn string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeMagicLink,
		To:   to,
		Data: magicLinkData(to, magicLink, expiresIn, time.Now()),
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
		Data: passwordResetData(to, resetURL, expiresIn, time.Now()),
	})
	return err
}

// SendEmailVerification sends a branded verification code. The recipient
// enters it on the /verify-email page (or the evs.run site claim) to prove
// they own the address. expiresIn is a human-readable label (e.g. "10
// minutes"). No clickable link is included because the flows require the
// code regardless.
func (s *EmailService) SendEmailVerification(ctx context.Context, to, code, expiresIn string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeEmailVerification,
		To:   to,
		Data: verificationData(to, code, expiresIn, time.Now()),
	})
	return err
}

// roleLabel names an organization role for copy: "admin" -> "Admin".
func roleLabel(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return ""
	}
	return strings.ToUpper(role[:1]) + strings.ToLower(role[1:])
}

func invitationData(to, orgName, inviterName, inviterEmail, inviteLink, role string, expiresAt time.Time) map[string]interface{} {
	name, addr := strings.TrimSpace(inviterName), strings.TrimSpace(inviterEmail)
	inviter := name
	switch {
	case name != "" && addr != "":
		inviter = name + " (" + addr + ")"
	case name == "":
		inviter = addr
	}
	who := name
	if who == "" {
		who = addr
	}
	if who == "" {
		who = "A teammate"
	}
	return map[string]interface{}{
		"organization_name": strings.TrimSpace(orgName),
		"inviter":           inviter,
		"inviter_name":      who,
		"invite_link":       inviteLink,
		"role":              roleLabel(role),
		"account_email":     strings.TrimSpace(to),
		"expires_at":        FormatNoticeTime(expiresAt),
	}
}

// SendInvitation sends an organization invitation email.
// role and expiresAt describe the invitation; empty or zero hides the row.
func (s *EmailService) SendInvitation(ctx context.Context, to, orgName, inviterName, inviterEmail, inviteLink, role string, expiresAt time.Time) error {
	_, err := s.SendEmail(ctx, s.invitationRequest(to, orgName, inviterName, inviterEmail, inviteLink, role, expiresAt))
	return err
}

// invitationRequest builds the invitation. Its headers carry nothing the
// inviter chose: the From name is always "Everstack" and Reply-To stays the
// invites sender's own. An inviter's display name in the From header let
// anyone send mail that an inbox shows as, say, "PayPal Billing (via
// Everstack)", and Reply-To would have sent the recipient's answer to an
// address of the inviter's choosing. The inviter is named in the body only,
// where html/template escapes it.
func (s *EmailService) invitationRequest(to, orgName, inviterName, inviterEmail, inviteLink, role string, expiresAt time.Time) SendEmailRequest {
	return SendEmailRequest{
		Type:         EmailTypeInvitation,
		To:           to,
		Data:         invitationData(to, orgName, inviterName, inviterEmail, inviteLink, role, expiresAt),
		FromOverride: "Everstack <" + s.config.Senders.Invites.Email + ">",
	}
}

func welcomeData(to, userName string) map[string]interface{} {
	return map[string]interface{}{
		"user_name":     strings.TrimSpace(userName),
		"account_email": strings.TrimSpace(to),
	}
}

// SendCloudWelcome sends the Everstack Cloud welcome email. It also states
// the signup credit and that it expires 30 days after signup; a zero amount
// leaves that line out.
func (s *EmailService) SendCloudWelcome(ctx context.Context, to, userName string, starterCreditUSD float64) error {
	return s.SendCloudWelcomeOnce(ctx, to, userName, starterCreditUSD, "")
}

// SendCloudWelcomeOnce gives both identity backends the same provider key so
// a retried signup cannot deliver a second welcome.
func (s *EmailService) SendCloudWelcomeOnce(ctx context.Context, to, userName string, starterCreditUSD float64, idempotencyKey string) error {
	data := welcomeData(to, userName)
	if starterCreditUSD > 0 {
		data["starter_credit"] = formatWholeUSD(starterCreditUSD)
	}
	_, err := s.SendEmail(ctx, SendEmailRequest{Type: EmailTypeWelcome, To: to, Data: data, IdempotencyKey: idempotencyKey})
	return err
}

// formatWholeUSD renders a dollar amount without cents when it is whole ($5),
// with cents otherwise ($2.50).
func formatWholeUSD(usd float64) string {
	if usd == float64(int64(usd)) {
		return fmt.Sprintf("$%d", int64(usd))
	}
	return fmt.Sprintf("$%.2f", usd)
}

// SendWelcome sends a welcome email to a new user
func (s *EmailService) SendWelcome(ctx context.Context, to, userName string) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type: EmailTypeWelcome,
		To:   to,
		Data: welcomeData(to, userName),
	})
	return err
}

// BillingURL is the page a billing email should send the reader to. The cloud
// app nests billing under the organization slug; with no slug the link lands
// on the app root, which redirects to the user's org.
func BillingURL(cloudURL, orgSlug string) string {
	base := strings.TrimRight(cloudURL, "/")
	if base == "" {
		base = "https://app.everstack.ai"
	}
	slug := strings.Trim(strings.TrimSpace(orgSlug), "/")
	if slug == "" {
		return base
	}
	return base + "/" + slug + "/settings/billing"
}

// PaymentSuccessParams carries optional context for payment receipts.
// All fields except OrgName + Amount are optional — empty values are gracefully
// hidden in the template rather than rendering as blank rows.
type PaymentSuccessParams struct {
	To             string
	IdempotencyKey string // optional provider idempotency key (see SendEmailRequest)
	OrgName        string
	OrgSlug        string    // builds the billing link; empty falls back to the app root
	Amount         float64   // major units (dollars for USD), formatted with 2 decimals
	Currency       string    // ISO code, e.g. "usd"; empty means USD
	PlanTier       string    // e.g. "Pro", "Team"; omitted if empty
	BillingPeriod  string    // e.g. "May 1 to May 31, 2026"; omitted if empty
	InvoiceNumber  string    // e.g. "INV-2026-0042"; omitted if empty
	InvoiceURL     string    // Stripe-hosted invoice URL; button hidden if empty
	PaidAt         time.Time // zero hides the row
}

func paymentSuccessData(p PaymentSuccessParams) map[string]interface{} {
	return map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"amount":            moneyWithCode(p.Amount, p.Currency),
		"plan_tier":         p.PlanTier,
		"billing_period":    p.BillingPeriod,
		"invoice_number":    p.InvoiceNumber,
		"invoice_url":       p.InvoiceURL,
		"changed_at":        FormatNoticeTime(p.PaidAt),
	}
}

// SendPaymentSuccess sends a payment confirmation receipt.
func (s *EmailService) SendPaymentSuccess(ctx context.Context, p PaymentSuccessParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type:           EmailTypePaymentSuccess,
		To:             p.To,
		IdempotencyKey: p.IdempotencyKey,
		Data:           paymentSuccessData(p),
	})
	return err
}

// PaymentFailedParams carries optional context for payment-failure emails.
type PaymentFailedParams struct {
	To             string
	IdempotencyKey string // optional provider idempotency key (see SendEmailRequest)
	OrgName        string
	OrgSlug        string  // builds the billing link; empty falls back to the app root
	Amount         float64 // major units; pass 0 to hide the amount in the copy
	Currency       string  // ISO code, e.g. "usd"; empty means USD
	Reason         string  // human-readable decline reason; panel hidden if empty
	PaymentURL     string  // direct pay/update-card link (Stripe hosted invoice); falls back to the billing page
	NextRetryAt    string  // pre-formatted date/time, e.g. "May 3 at 2:00 PM UTC" — line hidden if empty
	GracePeriodEnd string  // pre-formatted date, e.g. "May 8" — line hidden if empty
}

// SendPaymentFailed sends a payment failure notification.
func (s *EmailService) SendPaymentFailed(ctx context.Context, p PaymentFailedParams) error {
	data := map[string]interface{}{
		"organization_name": p.OrgName,
		"org_slug":          p.OrgSlug,
		"failure_reason":    p.Reason,
		"payment_url":       p.PaymentURL,
		"next_retry_at":     p.NextRetryAt,
		"grace_period_end":  p.GracePeriodEnd,
	}
	if p.Amount > 0 {
		data["amount"] = FormatMoney(p.Amount, p.Currency)
	}
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type:           EmailTypePaymentFailed,
		To:             p.To,
		IdempotencyKey: p.IdempotencyKey,
		Data:           data,
	})
	return err
}

// UsageWarningParams carries optional context for usage-warning emails.
type UsageWarningParams struct {
	To           string
	OrgName      string
	OrgSlug      string  // builds the billing link; empty falls back to the app root
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
			"org_slug":          p.OrgSlug,
			"usage_percent":     fmt.Sprintf("%.0f", p.UsagePercent),
			"plan_tier":         p.PlanTier,
			"current_usage":     p.CurrentUsage,
			"limit":             p.Limit,
			"period_end":        p.PeriodEnd,
		},
	})
	return err
}

// LowBalanceParams describes a prepaid credit wallet that has crossed a
// warning threshold. Amounts are in major units of Currency.
type LowBalanceParams struct {
	To               string
	IdempotencyKey   string // optional provider idempotency key (see SendEmailRequest)
	OrgName          string
	OrgSlug          string    // builds the add-credit link; empty falls back to the app root
	Available        float64   // spendable credit right now (balance minus in-flight holds), floored at 0 in copy
	Funded           float64   // the most recent grant or top-up, the reference the threshold is a fraction of
	ThresholdPercent int       // the threshold that was crossed, e.g. 25 or 10; 0 when Depleted
	Currency         string    // ISO code; empty means USD
	Depleted         bool      // true when nothing is left: requests are declined until credit is added
	CheckedAt        time.Time // when the balance crossed the threshold; zero hides the row
}

func lowBalanceData(p LowBalanceParams) map[string]interface{} {
	available := p.Available
	if available < 0 {
		available = 0
	}
	remainingPercent := 0
	if p.Funded > 0 && available > 0 {
		remainingPercent = int(available / p.Funded * 100)
		if remainingPercent > 100 {
			remainingPercent = 100
		}
	}
	return map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"available":         FormatMoney(available, p.Currency),
		"funded":            FormatMoney(p.Funded, p.Currency),
		"threshold_percent": fmt.Sprintf("%d", p.ThresholdPercent),
		"remaining_percent": fmt.Sprintf("%d", remainingPercent),
		"depleted":          p.Depleted,
		"changed_at":        FormatNoticeTime(p.CheckedAt),
	}
}

// SendLowBalance sends a low (or exhausted) prepaid credit warning.
func (s *EmailService) SendLowBalance(ctx context.Context, p LowBalanceParams) error {
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type:           EmailTypeCreditLowBalance,
		To:             p.To,
		IdempotencyKey: p.IdempotencyKey,
		Data:           lowBalanceData(p),
	})
	return err
}

// InstanceLimitStage selects which of the three instance-limit emails to send.
type InstanceLimitStage string

const (
	InstanceLimitStageOverLimit InstanceLimitStage = "over_limit"
	InstanceLimitStageReminder  InstanceLimitStage = "reminder"
	InstanceLimitStagePaused    InstanceLimitStage = "paused"
)

// InstanceLimitParams describes an organization that holds more managed
// instances than its plan allows (docs/design/instance-over-limit.md).
type InstanceLimitParams struct {
	To             string
	IdempotencyKey string // optional provider idempotency key (see SendEmailRequest)
	OrgName        string
	OrgSlug        string // builds the instances and billing links
	Stage          InstanceLimitStage
	InstanceLimit  int
	SlotsUsed      int
	GraceEndsAt    time.Time
	KeptSlugs      []string // Stage paused: instances still running
	PausedSlugs    []string // Stage paused: instances the platform paused
	// ExtraInstancePriceUSD is the monthly fee for an instance beyond the
	// plan, paid from prepaid credit; 0 hides the row.
	ExtraInstancePriceUSD float64
	NoticedAt             time.Time // when Everstack noticed (or paused); zero hides the row
}

func instanceLimitData(p InstanceLimitParams, instancesURL string) map[string]interface{} {
	cost := ""
	if p.ExtraInstancePriceUSD > 0 {
		cost = FormatMoney(p.ExtraInstancePriceUSD, "usd") + " per extra instance per month, from prepaid credit"
	}
	return map[string]interface{}{
		"organization_name": orgDisplayName(p.OrgName),
		"org_slug":          p.OrgSlug,
		"instance_limit":    fmt.Sprintf("%d", p.InstanceLimit),
		"limit_label":       pluralize(p.InstanceLimit, "instance", "instances"),
		"slots_used":        fmt.Sprintf("%d", p.SlotsUsed),
		"grace_ends_at":     FormatNoticeTime(p.GraceEndsAt),
		"instances_url":     instancesURL,
		"kept":              strings.Join(p.KeptSlugs, ", "),
		"paused":            strings.Join(p.PausedSlugs, ", "),
		"plural":            p.InstanceLimit != 1,
		"extra_cost":        cost,
		"changed_at":        FormatNoticeTime(p.NoticedAt),
	}
}

// SendInstanceLimit sends the over-limit, reminder or enforcement email.
func (s *EmailService) SendInstanceLimit(ctx context.Context, p InstanceLimitParams) error {
	var typ EmailType
	switch p.Stage {
	case InstanceLimitStageReminder:
		typ = EmailTypeInstanceOverLimitReminder
	case InstanceLimitStagePaused:
		typ = EmailTypeInstancesPaused
	default:
		typ = EmailTypeInstanceOverLimit
	}
	cloudURL := strings.TrimRight(s.config.CloudURL, "/")
	if cloudURL == "" {
		cloudURL = "https://app.everstack.ai"
	}
	instancesURL := cloudURL
	if slug := strings.Trim(strings.TrimSpace(p.OrgSlug), "/"); slug != "" {
		instancesURL = cloudURL + "/" + slug + "/instances"
	}
	_, err := s.SendEmail(ctx, SendEmailRequest{
		Type:           typ,
		To:             p.To,
		IdempotencyKey: p.IdempotencyKey,
		Data:           instanceLimitData(p, instancesURL),
	})
	return err
}

// logoDarkURL is the light wordmark that replaces the logo in dark mode. A
// configured LogoDarkURL wins; the built-in logo has a built-in light
// variant; a custom logo without one gets none (it keeps its light chip in
// dark mode, see templates.logoBlock).
func logoDarkURL(cfg *config.Config, logoURL interface{}) string {
	if cfg != nil && cfg.LogoDarkURL != "" {
		return cfg.LogoDarkURL
	}
	if u, _ := logoURL.(string); u == config.DefaultLogoURL {
		return config.DefaultLogoDarkURL
	}
	return ""
}

func emailSendFailureLog(kind types.EmailType, to string, err error) string {
	classification := "provider error"
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		classification = "timeout"
	} else {
		var status int
		if _, scanErr := fmt.Sscanf(err.Error(), "resend API error (%d):", &status); scanErr == nil && status >= 100 && status <= 599 {
			classification = fmt.Sprintf("provider status %d", status)
		}
	}
	return fmt.Sprintf("email: send failed type=%s domain=%s error=%s", kind, recipientDomain(to), classification)
}
