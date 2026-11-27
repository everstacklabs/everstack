package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// The invoice receipt and the low or used-up prepaid credit warning, on the
// shared notice layout. Data keys (service.paymentSuccessData,
// service.lowBalanceData):
//
//	organization_name   the organization's display name
//	amount              receipt: the amount paid, with its currency code
//	plan_tier, billing_period, invoice_number, invoice_url
//	available, funded   low balance: credit left and the last top-up or grant
//	threshold_percent, depleted
//	changed_at          when it was paid, or when the balance was checked (UTC)

var billingNoticeTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypePaymentSuccess: {
		Subject: "Receipt: {{.amount}} paid by the {{.organization_name}} organization",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Payment receipt",
			Heading: "Payment received" + badge(lcBadgeGreen, "Paid"),
			Rows: rowTotal("Amount paid", "amount") +
				row("Plan", "plan_tier") +
				row("Billing period", "billing_period") +
				rowMono("Invoice", "invoice_number") +
				row("Organization", "organization_name") +
				row("Paid", "changed_at"),
			Body:     para(`Everstack received <strong>{{.amount}}</strong> from the <strong>{{.organization_name}}</strong> organization{{if .plan_tier}} for the {{.plan_tier}} plan{{end}}.`),
			Next:     para(`Nothing else to do: everything keeps running, and the next invoice arrives at the start of the next billing period.`),
			Security: securityLine(`If you don't recognise this charge, contact us at ` + billingMailto + `.`),
			Button: `{{if .invoice_url}}` + primaryButton("{{.invoice_url}}", "View invoice") +
				`{{else}}` + primaryButton("{{.billing_url}}", "Open billing") + `{{end}}`,
			Extra:  `{{if .invoice_url}}` + billingPageLine + `{{end}}`,
			Footer: billingFooter(""),
		}),
	},
	types.EmailTypeCreditLowBalance: {
		Subject: "{{if .depleted}}The {{.organization_name}} organization is out of Everstack credit" +
			"{{else}}The {{.organization_name}} organization has {{.available}} of Everstack credit left{{end}}",
		HTMLTemplate: noticeLayout(notice{
			Title: "Credit balance",
			Heading: "{{if .depleted}}Prepaid credit used up" + badge(lcBadgeRed, "Action needed") +
				"{{else}}Prepaid credit running low" + badge(lcBadgeAmber, "Low balance") + "{{end}}",
			Rows: row("Available credit", "available") +
				row("Last added", "funded") +
				"{{if not .depleted}}<tr>" + lcLabel + "Warning level</td>" + lcValue + "Under {{.threshold_percent}}% of the last top-up</td></tr>{{end}}" +
				row("Organization", "organization_name") +
				row("Checked", "changed_at"),
			Body: "{{if .depleted}}" +
				para(`The <strong>{{.organization_name}}</strong> organization on Everstack has no prepaid credit left.`) +
				"{{else}}" +
				para(`The <strong>{{.organization_name}}</strong> organization on Everstack has <strong>{{.available}}</strong> of prepaid credit left, under {{.threshold_percent}}% of the {{.funded}} last added.`) +
				"{{end}}",
			Next: "{{if .depleted}}" +
				para(`Features that run on prepaid credit, such as starting new sandboxes, stop until credit is added; everything else keeps running. Nothing you have built is lost: add credit and everything resumes straight away.`) +
				"{{else}}" +
				para(`Everything is still running. When the balance reaches zero, features that run on prepaid credit, such as starting new sandboxes, stop until credit is added, so top up now if you would rather not be interrupted.`) +
				"{{end}}",
			Button: primaryButton("{{.billing_url}}", "Add credit"),
			Footer: billingFooter(""),
		}),
	},
}
