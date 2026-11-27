package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// Auto top-up emails (docs/design/auto-top-up.md), in the shared notice
// layout. Data keys (built by service.autoTopUpData):
//
//	organization_name   the organization's display name
//	amount              the auto top-up amount
//	card                the card label ("Visa ending in 4242"); may be empty
//	balance             spendable credit after the charge; may be empty
//	remaining_cap, monthly_cap   what is left of the monthly limit; may be empty
//	reason              failed: what happened and what to do
//	no_card, paused     failed variants
//	pause_reason        "dispute" or "refund": paused without a charge
//	next_attempt, changed_at     UTC times; may be empty
//	billing_url, logo_url

var autoTopUpTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypeAutoTopUpCharged: {
		Subject: "Auto top-up charged {{.amount}} for the {{.organization_name}} organization",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Auto top-up charged",
			Heading: "Auto top-up charged {{.amount}}" + badge(lcBadgeGreen, "Paid"),
			Rows: row("Credit added", "amount") +
				row("Charged to", "card") +
				row("New balance", "balance") +
				row("Left this month", "remaining_cap") +
				row("Monthly limit", "monthly_cap") +
				row("Organization", "organization_name") +
				row("Charged", "changed_at"),
			Body:     para(`Your balance dropped below the level you set, so we charged {{if .card}}{{.card}}{{else}}your default card{{end}} <strong>{{.amount}}</strong> and added it as prepaid credit to the <strong>{{.organization_name}}</strong> organization on Everstack.`),
			Next:     para(`Prepaid credit never expires. {{if .remaining_cap}}Auto top-up can charge up to {{.remaining_cap}} more this month. {{end}}You can change the amounts or turn auto top-up off in Billing.`),
			Security: securityLine(`If you didn't set up auto top-up, turn it off in Billing and contact us at ` + billingMailto + `.`),
			Button:   primaryButton("{{.billing_url}}", "Open billing"),
			Footer:   billingFooter(""),
		}),
	},
	types.EmailTypeAutoTopUpFailed: {
		Subject: "{{if .paused}}Auto top-up paused{{else}}Auto top-up failed{{end}}, action needed for the {{.organization_name}} organization",
		HTMLTemplate: noticeLayout(notice{
			Title: "Auto top-up failed",
			Heading: "{{if .paused}}Auto top-up paused" + badge(lcBadgeRed, "Action needed") +
				"{{else}}Auto top-up failed" + badge(lcBadgeAmber, "Action needed") + "{{end}}",
			Rows: row("Amount", "amount") +
				row("Card", "card") +
				row("Organization", "organization_name") +
				row("{{if .pause_reason}}Paused{{else}}Attempted{{end}}", "changed_at") +
				row("Next try", "next_attempt"),
			Body: "{{if .pause_reason}}" +
				para(`We paused auto top-up for the <strong>{{.organization_name}}</strong> organization on Everstack. {{.reason}}`) +
				"{{else}}" +
				para(`We tried to add <strong>{{.amount}}</strong> of prepaid credit to the <strong>{{.organization_name}}</strong> organization on Everstack{{if .card}} by charging {{.card}}{{end}}, but the payment did not go through. {{.reason}}`) +
				"{{end}}",
			Next: "{{if .pause_reason}}" +
				para(`Your current credit is not affected. When you are ready, an owner or admin can turn auto top-up back on in Billing. You can also buy credit yourself at any time.`) +
				"{{else if .paused}}" +
				para(`After several failed attempts, auto top-up is paused. Fix the card, then save your auto top-up settings in Billing to turn it back on. You can also buy credit yourself at any time.`) +
				"{{else if .next_attempt}}" +
				para(`We will try again after {{.next_attempt}}. Updating your default card makes the next try happen sooner. You can also buy credit yourself at any time.`) +
				"{{else}}" +
				para(`You can also buy credit yourself at any time.`) +
				"{{end}}",
			Button: primaryButton("{{.billing_url}}", "Fix in billing"),
			Footer: billingFooter(""),
		}),
	},
}
