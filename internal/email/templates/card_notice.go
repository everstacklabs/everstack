package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// Saved payment method emails (docs/design/payment-methods.md). They share
// the billing card layout of the receipt and lifecycle emails. Data keys
// (built by service.cardNoticeData):
//
//	organization_name  the organization's display name
//	method             "Visa ending in 4242", "Link (a***@gmail.com)",
//	                   "Apple Pay (Visa ending in 4242)"
//	noun, noun_title   "card"/"Card" for a plain card, else "payment method"
//	expires            "08/2029", may be empty
//	is_default         added: the method became the default
//	changed_at         "Oct 4, 2026 at 6:32 PM UTC", may be empty
//	changed_by         who made the change, may be empty
//	source_note        why a change made outside the billing page happened
//	remaining_note     what the organization can still be charged with
//	previous_default   default changed: the method it replaced, may be empty
//	billing_url, logo_url
//
// The organization is always named as such ("the Acme organization on
// Everstack", an "Organization" row), never as a bare line under the
// headline, where an organization called "everstack" read as our brand.

// cardLayout wraps a payment method email in the shared notice layout.
func cardLayout(title, heading, rows, body string, security bool) string {
	n := notice{
		Title: title, Heading: heading, Rows: rows, Body: body,
		Button: primaryButton("{{.billing_url}}", "Manage payment methods"),
		Footer: billingFooter("Everstack never sees or stores full card numbers."),
	}
	if security {
		n.Security = securityLine(billingSecurity)
	}
	return noticeLayout(n)
}

var cardTemplates = map[types.EmailType]*EmailTemplate{
	// Data keys (service.billingContactChangedData): organization_name,
	// org_slug, member (a name, or "An owner"), old_email, new_email,
	// changed_at, billing_url.
	types.EmailTypeBillingContactChanged: {
		Subject: "The billing email for the {{.organization_name}} organization changed",
		HTMLTemplate: noticeLayout(notice{
			Title: "Billing email changed", Heading: "Billing email changed",
			Rows: row("Organization", "organization_name") +
				row("New billing email", "new_email") +
				row("Previous billing email", "old_email") +
				row("Changed", "changed_at"),
			Body: lcPara + `
      {{.member}} changed their Everstack sign-in email. The billing email for the <strong>{{.organization_name}}</strong> organization was their previous sign-in email, so it changed with it. Receipts and billing notices for the {{.organization_name}} organization now go to <strong>{{.new_email}}</strong>.
    </p>
`,
			Security: securityLine("If you didn't expect this, an owner can set a different billing email in the organization's billing settings."),
			Button:   primaryButton("{{.billing_url}}", "Open billing settings"),
			Footer:   billingFooter("You are receiving this because you are an owner of this organization."),
		}),
	},
	types.EmailTypeCardAdded: {
		Subject: "{{.method}} added to the {{.organization_name}} organization",
		HTMLTemplate: cardLayout("Payment method added", "{{.noun_title}} added",
			row("Payment method", "method")+
				row("Organization", "organization_name")+
				row("Expires", "expires")+
				row("Added", "changed_at"),
			lcPara+`
      <strong>{{.method}}</strong> was added to the <strong>{{.organization_name}}</strong> organization on Everstack.{{if .is_default}} It is the default payment method, so future charges such as credit top-ups go to it.{{end}}
    </p>
`, true),
	},
	types.EmailTypeCardRemoved: {
		Subject: "{{.method}} removed from the {{.organization_name}} organization",
		HTMLTemplate: cardLayout("Payment method removed", "{{.noun_title}} removed",
			row("Payment method", "method")+
				row("Organization", "organization_name")+
				row("Removed by", "changed_by")+
				row("Removed", "changed_at"),
			lcPara+`
      <strong>{{.method}}</strong> was removed from the <strong>{{.organization_name}}</strong> organization on Everstack and will not be charged again.{{if .source_note}} {{.source_note}}{{end}}
    </p>
`+optPara("remaining_note"), true),
	},
	types.EmailTypeCardDefaultChanged: {
		Subject: "Default payment method changed for the {{.organization_name}} organization",
		HTMLTemplate: cardLayout("Default payment method changed", "Default payment method changed",
			row("New default", "method")+
				row("Previous default", "previous_default")+
				row("Organization", "organization_name")+
				row("Changed by", "changed_by")+
				row("Changed", "changed_at"),
			lcPara+`
      <strong>{{.method}}</strong> is now the default payment method for the <strong>{{.organization_name}}</strong> organization on Everstack. Future charges, such as credit top-ups, go to it.{{if .source_note}} {{.source_note}}{{end}}
    </p>
`, true),
	},
	types.EmailTypeCardExpiring: {
		Subject: "{{.method}} for the {{.organization_name}} organization expires soon",
		HTMLTemplate: cardLayout("Payment method expiring soon", `{{.noun_title}} expiring soon `+lcBadgeAmber+`Action needed</span>`,
			row("Payment method", "method")+
				row("Organization", "organization_name")+
				row("Expires", "expires"),
			lcPara+`
      <strong>{{.method}}</strong>, the default payment method for the <strong>{{.organization_name}}</strong> organization on Everstack, expires{{if .expires}} at the end of {{.expires}}{{else}} soon{{end}}. Add a new card and make it the default before then so charges such as credit top-ups keep working.
    </p>
    `+lcPara+`
      If your bank has already sent you a replacement card, the new expiry may update automatically. The billing page shows the date we have on file.
    </p>
`, false),
	},
}
