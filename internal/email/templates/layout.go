package templates

import "strings"

// The notice layout every transactional email shares. It is the design the
// payment method emails introduced: a precise headline, a detail table that
// names the subject, the organization, who acted and when, a one-sentence
// body, a "what happens next" line, an optional security line, one primary
// button, and a footer that says why the recipient got the email. The logo
// tag is swapped for the adaptive, dark-mode-safe logo by init (logoBlock).
//
// Templates compose these pieces instead of copying markup. Every style
// string is one darkModeStyleBlock already knows, so dark mode keeps working.

// notice describes one email. Each field is an html/template fragment.
type notice struct {
	Title    string // the <title>, plain text
	Heading  string // the headline, may end in a badge
	Rows     string // detail rows (row, rowMono, rowTotal)
	Body     string // body paragraphs (para, optPara)
	Next     string // what this means or what happens next (para)
	Security string // the security line (securityLine), optional
	Button   string // the one primary button (primaryButton)
	Extra    string // anything below the button, e.g. a link fallback
	Footer   string // why the recipient got this email (footer)
}

// noticeLayout renders a notice into a full HTML document.
func noticeLayout(n notice) string {
	table := ""
	if strings.TrimSpace(n.Rows) != "" {
		table = `
    ` + lcTable + `
      ` + n.Rows + `
    </table>
`
	}
	return `
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>` + n.Title + ` - Everstack</title>
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 40px 20px; background-color: #f4f4f5; color: #18181b;">
  <div style="max-width: 600px; margin: 0 auto; background: #ffffff; border-radius: 12px; padding: 40px; box-shadow: 0 2px 8px rgba(0,0,0,0.05);">
    <img src="{{.logo_url}}" alt="Everstack" style="height: 40px; margin-bottom: 28px;">

    <h1 style="color: #18181b; font-size: 22px; margin: 0 0 24px; font-weight: 600;">` + n.Heading + `</h1>
` + table + n.Body + n.Next + n.Security + n.Button + n.Extra + n.Footer + `
  </div>
</body>
</html>
`
}

// para is one body paragraph.
func para(html string) string {
	return `    ` + lcPara + `
      ` + html + `
    </p>
`
}

// optPara renders a body paragraph holding one data key, hidden when empty.
func optPara(key string) string {
	return `{{if .` + key + `}}` + lcPara + `{{.` + key + `}}</p>{{end}}
`
}

// securityLine is the "if this wasn't you" paragraph.
func securityLine(html string) string { return para(html) }

// billingSecurity is the security line of billing changes someone made.
const billingSecurity = `If you didn't make this change, remove access for anyone you don't recognise and contact us at ` + billingMailto + `.`

// billingMailto links the billing inbox.
const billingMailto = `<a href="mailto:billing@everstack.ai" style="color: #6366f1; text-decoration: none;">billing@everstack.ai</a>`

// supportMailto links the support inbox.
const supportMailto = `<a href="mailto:support@everstack.ai" style="color: #6366f1; text-decoration: none;">support@everstack.ai</a>`

// primaryButton is the one call to action. href is a template expression.
func primaryButton(href, label string) string {
	return `
    <a href="` + href + `" ` + lcButton + `>` + label + `</a>
`
}

// footer says why the recipient got the email.
func footer(html string) string {
	return `
    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 28px 0 0;">
      ` + html + `
    </p>`
}

// billingFooter is the footer of every email sent to billing managers.
// extra is an optional sentence placed before the contact line.
func billingFooter(extra string) string {
	if extra != "" {
		extra += " "
	}
	return footer(`You are receiving this because you manage billing for the {{.organization_name}} organization on Everstack. ` + extra +
		`Questions? Reply to this email or write to ` + billingMailto + `.`)
}

// billingPageLine points at the billing page when the button goes elsewhere
// (an invoice).
const billingPageLine = `
    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 20px 0 0;">
      Balance, usage and every past purchase are on the <a href="{{.billing_url}}" style="color: #6366f1; text-decoration: none;">billing page</a>.
    </p>`

// linkFallback prints a link's URL under its button for clients that drop
// buttons. key is the data key holding the URL.
func linkFallback(key string) string {
	return `
    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 20px 0 0;">
      If the button doesn't work, paste this link into your browser:<br>
      <span style="color: #6366f1; word-break: break-all;">{{.` + key + `}}</span>
    </p>`
}

// row renders one label/value table row, hidden when the value is empty.
func row(label, key string) string {
	return `{{if .` + key + `}}<tr>` + lcLabel + label + `</td>` + lcValue + `{{.` + key + `}}</td></tr>{{end}}`
}

// rowMono is row with a monospaced value, for references and ids.
func rowMono(label, key string) string {
	return `{{if .` + key + `}}<tr>` + lcLabel + label + `</td>` + lcValueMono + `{{.` + key + `}}</td></tr>{{end}}`
}

// rowTotal is an always-shown bold row, for the total of a receipt.
func rowTotal(label, key string) string {
	return `<tr><td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 600; border-bottom: 1px solid #f4f4f5;">` + label +
		`</td><td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 600; text-align: right; border-bottom: 1px solid #f4f4f5;">{{.` + key + `}}</td></tr>`
}

const (
	lcBadgeGreen = `<span style="display: inline-block; background: #ecfdf5; color: #047857; font-size: 11px; font-weight: 600; padding: 3px 9px; border-radius: 999px; letter-spacing: 0.04em; text-transform: uppercase; vertical-align: 3px; margin-left: 8px;">`
	lcBadgeAmber = `<span style="display: inline-block; background: #fffbeb; color: #b45309; font-size: 11px; font-weight: 600; padding: 3px 9px; border-radius: 999px; letter-spacing: 0.04em; text-transform: uppercase; vertical-align: 3px; margin-left: 8px;">`
	lcBadgeRed   = `<span style="display: inline-block; background: #fef2f2; color: #b91c1c; font-size: 11px; font-weight: 600; padding: 3px 9px; border-radius: 999px; letter-spacing: 0.04em; text-transform: uppercase; vertical-align: 3px; margin-left: 8px;">`
	lcPara       = `<p style="color: #18181b; font-size: 15px; line-height: 24px; margin: 0 0 16px;">`
	lcTable      = `<table cellpadding="0" cellspacing="0" border="0" style="width: 100%; border-collapse: collapse; border-top: 1px solid #e4e4e7; margin: 0 0 24px;">`
	lcLabel      = `<td style="padding: 14px 16px 14px 0; color: #71717a; font-size: 14px; border-bottom: 1px solid #f4f4f5; vertical-align: top; white-space: nowrap;">`
	lcValue      = `<td style="padding: 14px 0; color: #18181b; font-size: 14px; font-weight: 500; text-align: right; border-bottom: 1px solid #f4f4f5;">`
	lcValueMono  = `<td style="padding: 14px 0; color: #18181b; font-size: 13px; font-weight: 500; text-align: right; border-bottom: 1px solid #f4f4f5; font-family: 'SF Mono', Monaco, Menlo, monospace; word-break: break-all;">`
	lcButton     = `style="display: inline-block; background: #18181b; color: #ffffff; padding: 12px 28px; border-radius: 8px; text-decoration: none; font-weight: 500; font-size: 14px;"`
)

// badge closes a badge span opened by one of the lcBadge constants.
func badge(open, text string) string { return ` ` + open + text + `</span>` }
