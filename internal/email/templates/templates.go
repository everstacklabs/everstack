package templates

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/everstacklabs/everstack/internal/email/types"
)

// darkModeStyleBlock is injected into every template's <head> at init time.
// It uses CSS attribute substring selectors so the existing inline-style
// templates don't need to grow class attributes - each rule targets the
// specific inline-style fragment used in the templates and overrides it
// only when the user's email client honours prefers-color-scheme: dark.
//
// Client support: Apple Mail (macOS/iOS), Outlook web/Mac, Thunderbird,
// Yahoo, ProtonMail. Gmail web/mobile and Outlook for Windows ignore the
// media query and keep the light design - acceptable graceful fallback.
//
// !important is required on every override because inline styles have
// higher specificity than @media-block class rules.
const darkModeStyleBlock = `<style>
/* ─── Default (light mode) overrides applied to all clients ───────────── */

/* Logo asset is designed for dark backgrounds (white wordmark on transparent
   + white-bg icon containing a dark "E"). On light email backgrounds the
   wordmark would be invisible, so we invert by default. Dark mode removes
   the invert below so the natively-correct version renders on dark bg. */
img[alt="Everstack"] { filter: invert(1) hue-rotate(180deg); }

/* Monochrome CTAs: any indigo button (templates use #6366f1) becomes black
   in light mode. Dark mode flips to white-on-black below. */
[style*="background: #6366f1"] {
  background-color: #18181b !important;
  color: #ffffff !important;
}

/* ─── Dark mode (Apple Mail, iOS Mail, Outlook web/Mac, Yahoo, etc.) ──── */

@media (prefers-color-scheme: dark) {
  body, [style*="background-color: #f4f4f5"] {
    background-color: #0a0a0a !important;
    color: #fafafa !important;
  }
  /* Card / panel surfaces */
  [style*="background: #ffffff"] { background-color: #18181b !important; }
  [style*="background: #fafafa"] { background-color: #1f1f23 !important; }
  [style*="background: #f9fafb"] { background-color: #1f1f23 !important; }
  [style*="background: #f4f4f5"] { background-color: #27272a !important; }
  /* Text */
  [style*="color: #18181b"] { color: #fafafa !important; }
  [style*="color: #52525b"] { color: #d4d4d8 !important; }
  [style*="color: #71717a"] { color: #a1a1aa !important; }
  [style*="color: #a1a1aa"] { color: #71717a !important; }
  /* Borders / dividers */
  [style*="border: 1px solid #e4e4e7"] { border-color: #3f3f46 !important; }
  [style*="border-top: 1px solid #e4e4e7"] { border-top-color: #3f3f46 !important; }
  [style*="border-bottom: 1px solid #e4e4e7"] { border-bottom-color: #3f3f46 !important; }
  [style*="border-bottom: 1px solid #f4f4f5"] { border-bottom-color: #27272a !important; }
  /* Inline link colour - keep indigo as a soft accent (only buttons are forced monochrome) */
  [style*="color: #6366f1"] { color: #a5b4fc !important; }
  [style*="color: #4338ca"] { color: #a5b4fc !important; }
  [style*="background: #eef2ff"] { background-color: #1e1b4b !important; }
  /* Monochrome CTAs: black button (light mode) → white button (dark mode).
     Covers both the indigo-overridden buttons and the receipt's native dark CTA. */
  [style*="background: #6366f1"],
  [style*="background: #18181b; color: #ffffff"] {
    background-color: #fafafa !important;
    color: #18181b !important;
  }
  /* Status badges - keep semantic hue, swap to dark-tinted bg with bright fg */
  [style*="background: #ecfdf5"] { background-color: #064e3b !important; }
  [style*="color: #047857"] { color: #34d399 !important; }
  [style*="background: #fef2f2"] { background-color: #7f1d1d !important; }
  [style*="color: #b91c1c"] { color: #fca5a5 !important; }
  [style*="background: #fffbeb"] { background-color: #78350f !important; }
  [style*="color: #b45309"] { color: #fbbf24 !important; }
  /* Logo: un-invert in dark mode so the natively dark-designed asset renders correctly */
  img[alt="Everstack"] { filter: none !important; }
}
</style>`

// init prepends darkModeStyleBlock to every template's <head> so callers
// don't have to think about it. Templates that have no <head> are left alone.
func init() {
	for _, tmpl := range emailTemplates {
		if !strings.Contains(tmpl.HTMLTemplate, "<head>") {
			continue
		}
		tmpl.HTMLTemplate = strings.Replace(
			tmpl.HTMLTemplate,
			"<head>",
			"<head>"+darkModeStyleBlock,
			1,
		)
	}
}

// EmailTemplate represents an email template
type EmailTemplate struct {
	Subject      string
	HTMLTemplate string
}

// Render renders the template with the given data
func (t *EmailTemplate) Render(data map[string]interface{}) (subject string, html string, err error) {
	// Render subject
	subjectTmpl, err := template.New("subject").Parse(t.Subject)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse subject template: %w", err)
	}
	var subjectBuf bytes.Buffer
	if err := subjectTmpl.Execute(&subjectBuf, data); err != nil {
		return "", "", fmt.Errorf("failed to execute subject template: %w", err)
	}

	// Render HTML
	htmlTmpl, err := template.New("html").Parse(t.HTMLTemplate)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse HTML template: %w", err)
	}
	var htmlBuf bytes.Buffer
	if err := htmlTmpl.Execute(&htmlBuf, data); err != nil {
		return "", "", fmt.Errorf("failed to execute HTML template: %w", err)
	}

	return subjectBuf.String(), htmlBuf.String(), nil
}

// GetTemplate returns the template for a given email type
func GetTemplate(emailType types.EmailType) (*EmailTemplate, error) {
	template, ok := emailTemplates[emailType]
	if !ok {
		return nil, fmt.Errorf("unknown email type: %s", emailType)
	}
	return template, nil
}

var emailTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypeInvitation: {
		Subject: "You've been invited to join {{.organization_name}} on Everstack",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Invitation to Join {{.organization_name}}</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 32px;">
    
    <h1 style="color: #18181b; font-size: 24px; margin: 0 0 16px;">You're invited!</h1>
    
    <p style="color: #52525b; font-size: 16px; line-height: 24px; margin: 0 0 24px;">
      <strong>{{.inviter_name}}</strong> has invited you to join <strong>{{.organization_name}}</strong> on Everstack.
    </p>
    
    <a href="{{.invite_link}}" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 32px; border-radius: 8px; text-decoration: none; font-weight: 500;">
      Accept Invitation
    </a>
    
    <p style="color: #a1a1aa; font-size: 14px; margin: 32px 0 0;">
      If you didn't expect this invitation, you can ignore this email.
    </p>
  </div>
</body>
</html>
`,
	},
	types.EmailTypeWelcome: {
		Subject: "Welcome to Everstack, {{.user_name}}",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Welcome to Everstack</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5; color: #18181b;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 32px;">

    <h1 style="color: #18181b; font-size: 24px; margin: 0 0 16px; font-weight: 600;">Hi {{.user_name}} 👋</h1>

    <p style="color: #18181b; font-size: 16px; line-height: 24px; margin: 0 0 16px;">
      Thanks for signing up - I'm Arnab, founder of Everstack. Genuinely excited you're here.
    </p>

    <p style="color: #52525b; font-size: 16px; line-height: 24px; margin: 0 0 28px;">
      Everstack is the platform for running AI agents in isolated, observable sandboxes - bring your own LLM provider, deploy agents in seconds, and see exactly what they did and what it cost. No black boxes.
    </p>

    <h2 style="color: #18181b; font-size: 18px; margin: 0 0 16px; font-weight: 600;">Getting started - 5 minutes</h2>

    <table cellpadding="0" cellspacing="0" border="0" style="margin: 0 0 32px; width: 100%;">
      <tr>
        <td style="vertical-align: top; padding: 0 12px 16px 0; width: 28px;">
          <div style="background: #eef2ff; color: #4338ca; width: 28px; height: 28px; border-radius: 14px; text-align: center; line-height: 28px; font-size: 13px; font-weight: 600;">1</div>
        </td>
        <td style="vertical-align: top; padding: 0 0 16px 0; color: #18181b; font-size: 15px; line-height: 22px;">
          <strong>Connect a provider.</strong><br>
          <span style="color: #52525b;">Drop in your OpenAI, Anthropic, or any compatible API key. Everstack proxies, logs, and meters every call.</span>
        </td>
      </tr>
      <tr>
        <td style="vertical-align: top; padding: 0 12px 16px 0;">
          <div style="background: #eef2ff; color: #4338ca; width: 28px; height: 28px; border-radius: 14px; text-align: center; line-height: 28px; font-size: 13px; font-weight: 600;">2</div>
        </td>
        <td style="vertical-align: top; padding: 0 0 16px 0; color: #18181b; font-size: 15px; line-height: 22px;">
          <strong>Spin up your first agent.</strong><br>
          <span style="color: #52525b;">Pick from a template or define your own. It runs in a Firecracker microVM, fully isolated, ready in seconds.</span>
        </td>
      </tr>
      <tr>
        <td style="vertical-align: top; padding: 0 12px 16px 0;">
          <div style="background: #eef2ff; color: #4338ca; width: 28px; height: 28px; border-radius: 14px; text-align: center; line-height: 28px; font-size: 13px; font-weight: 600;">3</div>
        </td>
        <td style="vertical-align: top; padding: 0 0 0 0; color: #18181b; font-size: 15px; line-height: 22px;">
          <strong>Bring your team.</strong><br>
          <span style="color: #52525b;">Invite teammates to your workspace, share agents, see usage rolled up across the org.</span>
        </td>
      </tr>
    </table>

    <a href="{{.cloud_url}}" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 32px; border-radius: 8px; text-decoration: none; font-weight: 500; font-size: 15px;">
      Open Everstack →
    </a>

    <hr style="border: none; border-top: 1px solid #e4e4e7; margin: 36px 0 24px;">

    <p style="color: #52525b; font-size: 15px; line-height: 22px; margin: 0 0 12px;">
      <strong style="color: #18181b;">Stuck on something? Just hit reply.</strong> This email goes straight to me - I read every one and respond personally, especially in the early days.
    </p>

    <p style="color: #52525b; font-size: 15px; line-height: 22px; margin: 0 0 24px;">
      The docs live at <a href="{{.docs_url}}" style="color: #6366f1; text-decoration: none;">{{.docs_url}}</a> if you want to dig in deeper before opening the app.
    </p>

    <p style="color: #18181b; font-size: 15px; line-height: 22px; margin: 0;">
      - Arnab<br>
      <span style="color: #71717a; font-size: 13px;">Founder, Everstack</span>
    </p>
  </div>

  <p style="text-align: center; color: #a1a1aa; font-size: 12px; margin: 24px 0 0;">
    You're receiving this because you signed up at {{.cloud_url}}.
  </p>
</body>
</html>
`,
	},
	types.EmailTypePaymentSuccess: {
		Subject: "Receipt from Everstack - ${{.amount}}",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Payment receipt - Everstack</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5; color: #18181b;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 28px;">

    <h1 style="color: #18181b; font-size: 28px; margin: 0 0 8px; font-weight: 600;">
      ${{.amount}}
      <span style="display: inline-block; background: #ecfdf5; color: #047857; font-size: 11px; font-weight: 600; padding: 3px 9px; border-radius: 999px; letter-spacing: 0.04em; text-transform: uppercase; vertical-align: 6px; margin-left: 8px;">Paid</span>
    </h1>
    <p style="color: #71717a; font-size: 14px; margin: 0 0 28px;">Receipt for {{.organization_name}}</p>

    <table cellpadding="0" cellspacing="0" border="0" style="width: 100%; border-collapse: collapse; border-top: 1px solid #e4e4e7;">
      {{if .plan_tier}}
      <tr>
        <td style="padding: 14px 0; color: #71717a; font-size: 14px; border-bottom: 1px solid #f4f4f5;">Plan</td>
        <td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 500; text-align: right; border-bottom: 1px solid #f4f4f5;">{{.plan_tier}}</td>
      </tr>
      {{end}}
      {{if .billing_period}}
      <tr>
        <td style="padding: 14px 0; color: #71717a; font-size: 14px; border-bottom: 1px solid #f4f4f5;">Billing period</td>
        <td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 500; text-align: right; border-bottom: 1px solid #f4f4f5;">{{.billing_period}}</td>
      </tr>
      {{end}}
      {{if .invoice_number}}
      <tr>
        <td style="padding: 14px 0; color: #71717a; font-size: 14px; border-bottom: 1px solid #f4f4f5;">Invoice</td>
        <td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 500; text-align: right; border-bottom: 1px solid #f4f4f5; font-family: 'SF Mono', Monaco, monospace;">{{.invoice_number}}</td>
      </tr>
      {{end}}
      <tr>
        <td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 600;">Total paid</td>
        <td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 600; text-align: right;">${{.amount}}</td>
      </tr>
    </table>

    {{if .invoice_url}}
    <a href="{{.invoice_url}}" style="display: inline-block; background: #18181b; color: #ffffff; padding: 12px 28px; border-radius: 8px; text-decoration: none; font-weight: 500; font-size: 14px; margin-top: 28px;">
      View invoice →
    </a>
    {{end}}

    <p style="color: #52525b; font-size: 14px; line-height: 22px; margin: 32px 0 0;">
      Thanks for being on Everstack. Your services keep running uninterrupted, and your next bill will arrive at the start of the next period.
    </p>

    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 20px 0 0;">
      Questions about this charge? Reply to this email or reach <a href="mailto:billing@everstack.ai" style="color: #6366f1; text-decoration: none;">billing@everstack.ai</a>.
    </p>
  </div>

  <p style="text-align: center; color: #a1a1aa; font-size: 12px; margin: 24px 0 0;">
    Everstack · Manage your billing at <a href="{{.cloud_url}}/settings/billing" style="color: #a1a1aa;">{{.cloud_url}}/settings/billing</a>
  </p>
</body>
</html>
`,
	},
	types.EmailTypePaymentFailed: {
		Subject: "Heads up - your Everstack payment didn't go through",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Payment failed - Everstack</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5; color: #18181b;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 28px;">

    <h1 style="color: #18181b; font-size: 24px; margin: 0 0 12px; font-weight: 600;">
      Your last payment didn't go through
      <span style="display: inline-block; background: #fef2f2; color: #b91c1c; font-size: 11px; font-weight: 600; padding: 3px 9px; border-radius: 999px; letter-spacing: 0.04em; text-transform: uppercase; vertical-align: 4px; margin-left: 8px;">Action needed</span>
    </h1>

    <p style="color: #52525b; font-size: 16px; line-height: 24px; margin: 0 0 24px;">
      We tried to charge {{if .amount}}<strong>${{.amount}}</strong> {{end}}for <strong>{{.organization_name}}</strong> and it was declined. Nothing's broken yet - your services are still running - but we need an updated payment method to keep things that way.
    </p>

    <div style="background: #fafafa; border: 1px solid #e4e4e7; border-radius: 8px; padding: 16px 20px; margin: 0 0 24px;">
      <p style="margin: 0 0 6px; color: #71717a; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600;">Reason from your bank</p>
      <p style="margin: 0; color: #18181b; font-size: 15px; line-height: 22px;">{{.failure_reason}}</p>
    </div>

    <a href="{{.cloud_url}}/settings/billing" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 28px; border-radius: 8px; text-decoration: none; font-weight: 500; font-size: 15px;">
      Update payment method →
    </a>

    {{if .next_retry_at}}
    <p style="color: #52525b; font-size: 14px; line-height: 22px; margin: 24px 0 0;">
      We'll automatically retry on <strong>{{.next_retry_at}}</strong>. If it succeeds, you don't have to do anything.
    </p>
    {{end}}

    {{if .grace_period_end}}
    <p style="color: #52525b; font-size: 14px; line-height: 22px; margin: 12px 0 0;">
      If we still can't collect by <strong>{{.grace_period_end}}</strong>, your services will be paused. Updating the card now avoids that.
    </p>
    {{end}}

    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 28px 0 0;">
      Questions, dispute, or something looks wrong? Reply to this email - it goes to <a href="mailto:billing@everstack.ai" style="color: #6366f1; text-decoration: none;">billing@everstack.ai</a>.
    </p>
  </div>
</body>
</html>
`,
	},
	types.EmailTypeUsageWarning: {
		Subject: "{{.organization_name}} is at {{.usage_percent}}% of your {{.plan_tier}} plan",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Usage update - Everstack</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5; color: #18181b;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 28px;">

    <h1 style="color: #18181b; font-size: 22px; margin: 0 0 8px; font-weight: 600;">
      You're at {{.usage_percent}}% of your {{if .plan_tier}}{{.plan_tier}} plan{{else}}plan{{end}}
      <span style="display: inline-block; background: #fffbeb; color: #b45309; font-size: 11px; font-weight: 600; padding: 3px 9px; border-radius: 999px; letter-spacing: 0.04em; text-transform: uppercase; vertical-align: 3px; margin-left: 8px;">Usage update</span>
    </h1>
    <p style="color: #71717a; font-size: 14px; margin: 0 0 24px;">{{.organization_name}}</p>

    <!-- Progress bar - table-based for max email-client compatibility -->
    <table cellpadding="0" cellspacing="0" border="0" style="width: 100%; margin: 0 0 8px;">
      <tr>
        <td style="background: #f4f4f5; border-radius: 999px; height: 10px; padding: 0;">
          <table cellpadding="0" cellspacing="0" border="0" style="width: {{.usage_percent}}%; height: 10px;">
            <tr><td style="background: #f59e0b; border-radius: 999px; height: 10px; line-height: 10px;">&nbsp;</td></tr>
          </table>
        </td>
      </tr>
    </table>
    <table cellpadding="0" cellspacing="0" border="0" style="width: 100%; margin: 0 0 28px;">
      <tr>
        <td style="color: #71717a; font-size: 12px;">{{if .current_usage}}{{.current_usage}}{{else}}Used{{end}}</td>
        <td style="color: #71717a; font-size: 12px; text-align: right;">{{if .limit}}of {{.limit}}{{else}}Limit{{end}}</td>
      </tr>
    </table>

    <p style="color: #18181b; font-size: 15px; line-height: 24px; margin: 0 0 16px;">
      Heads up: you've used about <strong>{{.usage_percent}}%</strong> of what's included this period{{if .period_end}}, which ends <strong>{{.period_end}}</strong>{{end}}. You're still fully running - this is a friendly nudge so you're not surprised.
    </p>

    <p style="color: #52525b; font-size: 15px; line-height: 24px; margin: 0 0 28px;">
      A few options from here:
    </p>

    <table cellpadding="0" cellspacing="0" border="0" style="width: 100%; margin: 0 0 28px;">
      <tr>
        <td style="vertical-align: top; padding: 0 12px 12px 0; width: 8px; color: #6366f1; font-size: 18px; line-height: 18px;">·</td>
        <td style="vertical-align: top; padding: 0 0 12px 0; color: #18181b; font-size: 15px; line-height: 22px;">
          <strong>Do nothing</strong> - most plans allow overage at a per-unit rate. Check the billing page for what that looks like for you.
        </td>
      </tr>
      <tr>
        <td style="vertical-align: top; padding: 0 12px 12px 0; color: #6366f1; font-size: 18px; line-height: 18px;">·</td>
        <td style="vertical-align: top; padding: 0 0 12px 0; color: #18181b; font-size: 15px; line-height: 22px;">
          <strong>Upgrade your plan</strong> - bigger included limits, lower per-unit rates above them, prorated to the rest of the period.
        </td>
      </tr>
      <tr>
        <td style="vertical-align: top; padding: 0 12px 0 0; color: #6366f1; font-size: 18px; line-height: 18px;">·</td>
        <td style="vertical-align: top; padding: 0 0 0 0; color: #18181b; font-size: 15px; line-height: 22px;">
          <strong>Set a hard cap</strong> - pause services automatically if you'd rather not pay overage. Configurable in billing settings.
        </td>
      </tr>
    </table>

    <a href="{{.cloud_url}}/settings/billing" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 28px; border-radius: 8px; text-decoration: none; font-weight: 500; font-size: 15px;">
      Open billing →
    </a>

    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 28px 0 0;">
      Questions on plans or limits? Reply to this email - it goes to <a href="mailto:billing@everstack.ai" style="color: #6366f1; text-decoration: none;">billing@everstack.ai</a>.
    </p>
  </div>
</body>
</html>
`,
	},
	types.EmailTypeMagicLink: {
		Subject: "Your Everstack sign-in link",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Your Everstack sign-in link</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 32px;">

    <h1 style="color: #18181b; font-size: 24px; margin: 0 0 16px;">Sign in to Everstack</h1>

    <p style="color: #52525b; font-size: 16px; line-height: 24px; margin: 0 0 24px;">
      Click the button below to sign in. This link expires in {{.expires_in}} and can be used once.
    </p>

    <a href="{{.magic_link}}" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 32px; border-radius: 8px; text-decoration: none; font-weight: 500;">
      Sign in to Everstack
    </a>

    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 32px 0 0;">
      If the button doesn't work, paste this URL into your browser:<br>
      <span style="color: #6366f1; word-break: break-all;">{{.magic_link}}</span>
    </p>

    <p style="color: #a1a1aa; font-size: 13px; line-height: 20px; margin: 24px 0 0;">
      Didn't request this? You can safely ignore this email - your account is not affected.
    </p>
  </div>
</body>
</html>
`,
	},
	types.EmailTypePasswordReset: {
		Subject: "Reset your Everstack password",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Reset your Everstack password</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 32px;">

    <h1 style="color: #18181b; font-size: 24px; margin: 0 0 16px;">Reset your password</h1>

    <p style="color: #52525b; font-size: 16px; line-height: 24px; margin: 0 0 24px;">
      Click the button below to choose a new password. This link expires in {{.expires_in}} and can be used once.
    </p>

    <a href="{{.reset_link}}" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 32px; border-radius: 8px; text-decoration: none; font-weight: 500;">
      Reset password
    </a>

    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 32px 0 0;">
      If the button doesn't work, paste this URL into your browser:<br>
      <span style="color: #6366f1; word-break: break-all;">{{.reset_link}}</span>
    </p>

    <p style="color: #a1a1aa; font-size: 13px; line-height: 20px; margin: 24px 0 0;">
      Didn't request this? You can safely ignore this email - your password will stay the same.
    </p>
  </div>
</body>
</html>
`,
	},
	types.EmailTypeEmailVerification: {
		Subject: "Verify your Everstack email",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Verify your Everstack email</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 32px;">

    <h1 style="color: #18181b; font-size: 24px; margin: 0 0 16px;">Verify your email</h1>

    <p style="color: #52525b; font-size: 16px; line-height: 24px; margin: 0 0 24px;">
      Enter the code below to confirm this email address. The code expires in {{.expires_in}}.
    </p>

    <div style="background: #f9fafb; border: 1px solid #e4e4e7; border-radius: 8px; padding: 20px; margin: 0 0 24px; text-align: center;">
      <p style="color: #71717a; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; margin: 0 0 8px; font-weight: 600;">Verification code</p>
      <p style="color: #18181b; font-size: 28px; font-family: 'SF Mono', Monaco, 'Menlo', monospace; margin: 0; letter-spacing: 0.2em; font-weight: 600;">
        {{.code}}
      </p>
    </div>

    <p style="color: #a1a1aa; font-size: 13px; line-height: 20px; margin: 24px 0 0;">
      Didn't request this? You can safely ignore this email - no changes have been made to your account.
    </p>
  </div>
</body>
</html>
`,
	},
	types.EmailTypeActivationToken: {
		Subject: "Your Gateway Activation Token - {{.plan_tier}} Plan",
		HTMLTemplate: `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Gateway Activation Token</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 32px;">
    
    <h1 style="color: #18181b; font-size: 24px; margin: 0 0 16px;">Your Gateway Activation Token</h1>
    
    <p style="color: #52525b; font-size: 16px; line-height: 24px; margin: 0 0 24px;">
      Your payment for the <strong>{{.plan_tier}}</strong> plan was successful! We couldn't automatically activate your gateway, so please use the activation token below to manually activate it.
    </p>
    
    <div style="background: #f9fafb; border: 1px solid #e5e7eb; border-radius: 8px; padding: 16px; margin: 0 0 24px;">
      <p style="color: #6b7280; font-size: 12px; text-transform: uppercase; margin: 0 0 8px;">Activation Token</p>
      <p style="color: #18181b; font-size: 14px; font-family: 'SF Mono', 'Monaco', 'Menlo', monospace; margin: 0; word-break: break-all;">
        {{.activation_token}}
      </p>
    </div>
    
    <h2 style="color: #18181b; font-size: 18px; margin: 32px 0 16px;">How to Activate</h2>
    
    <ol style="color: #52525b; font-size: 14px; line-height: 24px; padding-left: 20px; margin: 0 0 24px;">
      <li style="margin-bottom: 8px;">Go to your gateway admin panel{{if .gateway_url}} at <a href="{{.gateway_url}}" style="color: #6366f1;">{{.gateway_url}}</a>{{end}}</li>
      <li style="margin-bottom: 8px;">Navigate to <strong>Settings → Billing</strong></li>
      <li style="margin-bottom: 8px;">Click <strong>"Enter Activation Token"</strong></li>
      <li style="margin-bottom: 8px;">Paste the token above and click <strong>Activate</strong></li>
    </ol>
    
    <p style="color: #a1a1aa; font-size: 14px; margin: 32px 0 0;">
      This token expires in 1 hour. If you need a new token, please contact support at <a href="mailto:support@everstack.ai" style="color: #6366f1;">support@everstack.ai</a>
    </p>
  </div>
</body>
</html>
`,
	},
}
