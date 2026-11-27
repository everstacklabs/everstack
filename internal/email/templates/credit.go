package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// Prepaid credit emails: the top-up receipt (paid at checkout, or approved
// after review) and a refund, dispute or failed refund against a top-up. Data
// keys (built by service.creditPurchaseData and service.creditReversalData):
//
//	organization_name   the organization's display name
//	credit_added, tax, total, balance   formatted amounts; total names its currency
//	changed_by, changed_at              who and when (UTC); may be empty
//	approved            the purchase was held for review, then approved
//	amount, credit      reversal: the charge amount and the credit moved
//	amount_label, credit_label, by_label, at_label   reversal row labels
//	invoice_url, billing_url, logo_url

var creditTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypeCreditPurchased: {
		Subject: "Receipt: {{.credit_added}} of credit added to the {{.organization_name}} organization",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Credit receipt",
			Heading: "{{.credit_added}} of credit added" + badge(lcBadgeGreen, "Paid"),
			Rows: row("Credit added", "credit_added") +
				row("Tax", "tax") +
				rowTotal("Total charged", "total") +
				row("New balance", "balance") +
				row("Organization", "organization_name") +
				row("Purchased by", "changed_by") +
				row("Paid", "changed_at"),
			Body:     para(`<strong>{{.credit_added}}</strong> of prepaid credit was added to the <strong>{{.organization_name}}</strong> organization on Everstack{{if .approved}} after we reviewed the payment{{end}}.`),
			Next:     para(`{{if .approved}}The payment was held for review before the credit was added, which is why this receipt comes later than the charge. {{end}}Prepaid credit never expires and is drawn down as the organization uses Everstack.`),
			Security: securityLine(`If you didn't make this purchase, contact us at ` + billingMailto + `.`),
			Button: `{{if .invoice_url}}` + primaryButton("{{.invoice_url}}", "View invoice") +
				`{{else}}` + primaryButton("{{.billing_url}}", "Open billing") + `{{end}}`,
			Extra:  `{{if .invoice_url}}` + billingPageLine + `{{end}}`,
			Footer: billingFooter(""),
		}),
	},
	types.EmailTypeCreditReversed: {
		Subject: "{{if .refund}}Refund issued: {{.credit}} of credit removed from the {{.organization_name}} organization" +
			"{{else if .dispute_opened}}Payment disputed: {{.credit}} of credit on hold for the {{.organization_name}} organization" +
			"{{else if .dispute_won}}Dispute resolved: {{.credit}} of credit released for the {{.organization_name}} organization" +
			"{{else if .refund_failed}}Refund failed: {{.credit}} of credit restored to the {{.organization_name}} organization" +
			"{{else}}Dispute closed: {{.credit}} of credit removed from the {{.organization_name}} organization{{end}}",
		HTMLTemplate: noticeLayout(notice{
			Title: "Credit update",
			Heading: "{{if .refund}}Refund issued" + badge(lcBadgeAmber, "Refund") +
				"{{else if .dispute_opened}}A payment was disputed" + badge(lcBadgeRed, "Disputed") +
				"{{else if .dispute_won}}Dispute resolved" + badge(lcBadgeGreen, "Released") +
				"{{else if .refund_failed}}Refund failed" + badge(lcBadgeAmber, "Credit restored") +
				"{{else}}Dispute closed" + badge(lcBadgeRed, "Credit removed") + "{{end}}",
			Rows: row("{{.amount_label}}", "amount") +
				row("{{.credit_label}}", "credit") +
				row("New balance", "balance") +
				row("Organization", "organization_name") +
				row("{{.by_label}}", "changed_by") +
				row("{{.at_label}}", "changed_at"),
			Body: "{{if .refund}}" +
				para(`{{.amount}} was refunded to the original payment method, and the matching <strong>{{.credit}}</strong> of prepaid credit was removed from the <strong>{{.organization_name}}</strong> organization on Everstack.`) +
				"{{else if .dispute_opened}}" +
				para(`Your bank told us a {{.amount}} credit purchase for the <strong>{{.organization_name}}</strong> organization on Everstack is disputed, so <strong>{{.credit}}</strong> of credit is on hold.`) +
				"{{else if .dispute_won}}" +
				para(`The dispute on a {{.amount}} credit purchase for the <strong>{{.organization_name}}</strong> organization on Everstack was resolved in favor of the charge.`) +
				"{{else if .refund_failed}}" +
				para(`A {{.amount}} refund to the original payment method did not go through, so the <strong>{{.credit}}</strong> of prepaid credit removed for it is back on the <strong>{{.organization_name}}</strong> organization on Everstack.`) +
				"{{else}}" +
				para(`The bank decided the dispute on a {{.amount}} credit purchase for the <strong>{{.organization_name}}</strong> organization on Everstack in the cardholder's favor, so the matching <strong>{{.credit}}</strong> of prepaid credit was removed.`) +
				"{{end}}",
			Next: "{{if .refund}}" +
				para(`Refunds usually reach the account within 5 to 10 business days.`) +
				"{{else if .dispute_opened}}" +
				para(`Held credit cannot be spent while the bank reviews the dispute. If you did not mean to dispute this charge, reply to this email and we will help you resolve it with your bank.`) +
				"{{else if .dispute_won}}" +
				para(`The <strong>{{.credit}}</strong> of credit that was on hold is released and available to spend again.`) +
				"{{else if .refund_failed}}" +
				para(`No money was returned. If you still want this refund, reply to this email and we will help.`) +
				"{{else}}" +
				para(`The bank returned the funds to the cardholder. If you think this is a mistake, reply to this email.`) +
				"{{end}}",
			Security: "{{if .refund}}" + securityLine(`If you didn't ask for this refund, contact us at `+billingMailto+`.`) + "{{end}}",
			Button:   primaryButton("{{.billing_url}}", "Open billing"),
			Footer:   billingFooter(""),
		}),
	},
}
