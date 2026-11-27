package templates

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strings"
	texttemplate "text/template"
	"unicode"

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

/* Logo: see logoBlock. The default logo is the dark wordmark on a
   transparent background, shown as is in light mode. */

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
  /* Logo: swap to the light wordmark and drop the light chip behind it
     (only when a light variant exists; see logoBlock). */
  .evs-logo-chip { background-color: transparent !important; background-image: none !important; }
  .evs-logo-on-light { display: none !important; }
  .evs-logo-on-dark { display: block !important; width: 142px !important; height: 30px !important; max-height: none !important; }
}
</style>`

// optionalTemplateSets holds template sets that register themselves from
// their own file with registerTemplateSet, so no other file names them and
// the file can be left out of a build. They are filled by package-level
// variable initialisers, which all run before init.
var optionalTemplateSets []map[types.EmailType]*EmailTemplate

func registerTemplateSet(set map[types.EmailType]*EmailTemplate) bool {
	optionalTemplateSets = append(optionalTemplateSets, set)
	return true
}

// init prepends darkModeStyleBlock to every template's <head> so callers
// don't have to think about it. Templates that have no <head> are left alone.
func init() {
	// Templates kept in their own files register here, before the dark-mode
	// block is injected into every template.
	for typ, tmpl := range lifecycleTemplates {
		emailTemplates[typ] = tmpl
	}
	for typ, tmpl := range cardTemplates {
		emailTemplates[typ] = tmpl
	}
	for typ, tmpl := range creditTemplates {
		emailTemplates[typ] = tmpl
	}
	for typ, tmpl := range autoTopUpTemplates {
		emailTemplates[typ] = tmpl
	}
	for typ, tmpl := range spendLimitTemplates {
		emailTemplates[typ] = tmpl
	}
	for typ, tmpl := range billingNoticeTemplates {
		emailTemplates[typ] = tmpl
	}
	for typ, tmpl := range instanceLimitTemplates {
		emailTemplates[typ] = tmpl
	}
	for typ, tmpl := range accountTemplates {
		emailTemplates[typ] = tmpl
	}
	for _, set := range optionalTemplateSets {
		for typ, tmpl := range set {
			emailTemplates[typ] = tmpl
		}
	}
	for _, tmpl := range emailTemplates {
		tmpl.HTMLTemplate = logoTag.ReplaceAllStringFunc(tmpl.HTMLTemplate, func(tag string) string {
			return logoBlock(logoTag.FindStringSubmatch(tag)[1])
		})
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

// logoTag matches the logo every template writes. init replaces each with
// logoBlock, keeping its bottom margin.
var logoTag = regexp.MustCompile(`<img src="\{\{\.logo_url\}\}" alt="Everstack" style="height: 40px; margin-bottom: (\d+)px;">`)

// logoBlock renders the logo so it reads on light and dark backgrounds.
//
// logo_url is the dark wordmark on a transparent background. It sits on a
// small white chip whose colour is pinned with a background-image gradient:
// clients that recolour emails for dark mode without honouring
// prefers-color-scheme (the Gmail apps, Outlook.com) recolour background
// colours but leave background images alone, so the dark wordmark keeps a
// light backdrop there.
//
// When logo_dark_url (a light wordmark) is set, clients that do honour
// prefers-color-scheme (Apple Mail, iOS Mail, Outlook for Mac) drop the chip
// and swap to it via darkModeStyleBlock. A custom logo_url without a dark
// variant keeps its chip in dark mode instead.
//
// Size: the default assets (everstack-wordmark-{dark,light}-email.png) are
// 568x120, drawn at 142x30. The glyph fills 546x88 of that canvas, so it
// lands at 22px tall, and the rest is a transparent frame: 2.75px left and
// right, 4px top and bottom. The chip has no padding of its own; that frame
// is its padding, so the 6px corner radius only ever reaches transparent
// pixels and cannot clip the wordmark, and the glyph sits within 3px of the
// headline's left edge. 142x30 is the canvas's exact ratio (4.733), set as
// attributes as well as CSS because Outlook and Gmail honour only the
// attributes. Changing the asset means changing these numbers with it.
func logoBlock(marginBottom string) string {
	return `<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin: 0 0 ` + marginBottom + `px; border-collapse: separate;"><tr>` +
		`<td class="{{if .logo_dark_url}}evs-logo-chip{{else}}evs-logo-chip-fixed{{end}}" style="padding: 0; border-radius: 6px; background-color: #ffffff; background-image: linear-gradient(#ffffff, #ffffff);">` +
		`<img class="{{if .logo_dark_url}}evs-logo-on-light{{end}}" src="{{.logo_url}}" alt="Everstack" width="142" height="30" style="display: block; width: 142px; height: 30px; border: 0;">` +
		`{{if .logo_dark_url}}<img class="evs-logo-on-dark" src="{{.logo_dark_url}}" alt="Everstack" width="142" height="30" style="display: none; width: 0; height: 0; max-height: 0; overflow: hidden; mso-hide: all; border: 0;">{{end}}` +
		`</td></tr></table>`
}

// EmailTemplate represents an email template
type EmailTemplate struct {
	Subject      string
	HTMLTemplate string
}

// Render renders the template with the given data
func (t *EmailTemplate) Render(data map[string]interface{}) (subject string, html string, err error) {
	// The subject is a plain-text header, so it renders with text/template:
	// html/template would turn "O'Brien & Co" into "O&#39;Brien &amp; Co".
	// The body stays on html/template, which escapes every value.
	subjectTmpl, err := texttemplate.New("subject").Parse(t.Subject)
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

	return cleanSubject(subjectBuf.String()), htmlBuf.String(), nil
}

// MaxSubjectLength caps a rendered subject, in characters. Subjects carry
// organization, inviter and user names, which their owners choose.
const MaxSubjectLength = 160

// cleanSubject makes a rendered subject one line of printable text: control
// characters and bidirectional overrides and isolates (which can make a name
// read as something else) are dropped, runs of whitespace, line breaks
// included, become one space, and anything past MaxSubjectLength is cut.
func cleanSubject(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r),
			r >= 0x202A && r <= 0x202E,
			r >= 0x2066 && r <= 0x2069:
			return -1
		}
		return r
	}, raw)
	subject := strings.Join(strings.Fields(cleaned), " ")
	if runes := []rune(subject); len(runes) > MaxSubjectLength {
		subject = strings.TrimSpace(string(runes[:MaxSubjectLength-3])) + "..."
	}
	return subject
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
      We tried to charge {{if .amount}}<strong>{{.amount}}</strong> {{end}}for <strong>{{.organization_name}}</strong> and it was declined. Nothing's broken yet - your services are still running - but we need an updated payment method to keep things that way.
    </p>

    {{if .failure_reason}}
    <div style="background: #fafafa; border: 1px solid #e4e4e7; border-radius: 8px; padding: 16px 20px; margin: 0 0 24px;">
      <p style="margin: 0 0 6px; color: #71717a; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; font-weight: 600;">Reason from your bank</p>
      <p style="margin: 0; color: #18181b; font-size: 15px; line-height: 22px;">{{.failure_reason}}</p>
    </div>
    {{end}}

    <a href="{{if .payment_url}}{{.payment_url}}{{else}}{{.billing_url}}{{end}}" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 28px; border-radius: 8px; text-decoration: none; font-weight: 500; font-size: 15px;">
      Update payment method →
    </a>

    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 16px 0 0;">
      You can also manage cards and invoices any time at <a href="{{.billing_url}}" style="color: #6366f1; text-decoration: none;">{{.billing_url}}</a>.
    </p>

    {{if .next_retry_at}}
    <p style="color: #52525b; font-size: 14px; line-height: 22px; margin: 24px 0 0;">
      We'll automatically retry on <strong>{{.next_retry_at}}</strong>. If it succeeds, you don't have to do anything.
    </p>
    {{end}}

    {{if .grace_period_end}}
    <p style="color: #52525b; font-size: 14px; line-height: 22px; margin: 12px 0 0;">
      If we still can't collect by <strong>{{.grace_period_end}}</strong>, your subscription ends and your organization moves to the Starter plan, pausing any instances beyond its limit. Until then, new instances and plan changes are on hold. Updating the card now avoids that.
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

    <a href="{{.billing_url}}" style="display: inline-block; background: #6366f1; color: #ffffff; padding: 12px 28px; border-radius: 8px; text-decoration: none; font-weight: 500; font-size: 15px;">
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
}
