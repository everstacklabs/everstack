package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// Plan lifecycle templates (plan started, changed, set to end, renewing,
// ended), on the shared notice layout (layout.go). init() in templates.go
// merges them into emailTemplates before the dark-mode block is injected.

var lifecycleTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypeSubscriptionStarted: {
		Subject: "The {{.organization_name}} organization is on the Everstack {{.plan_name}} plan",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Plan started",
			Heading: "{{.plan_name}} plan started" + badge(lcBadgeGreen, "Active"),
			Rows: row("Plan", "plan_name") +
				row("Billing interval", "interval") +
				row("Price", "price") +
				row("Instances", "instances") +
				row("Seats", "seats") +
				row("Included credit", "included_credit") +
				row("Payment method", "payment_method") +
				row("Organization", "organization_name") +
				row("Started by", "changed_by") +
				row("Started", "changed_at") +
				row("Current period ends", "period_end"),
			Body: para(`The <strong>{{.plan_name}}</strong> plan is now active for the <strong>{{.organization_name}}</strong> organization on Everstack.`),
			Next: para(`Its limits apply straight away. Usage, invoices and the payment method are on the billing page at any time.`) +
				`{{if .self_hosted}}` + para(`This is a self-hosted license. To activate it, open the billing page and follow the activation steps for your instance. For your security, we never send activation tokens by email.`) + `{{end}}
`,
			Security: securityLine(billingSecurity),
			Button:   primaryButton("{{.billing_url}}", "Open billing"),
			Footer:   billingFooter(""),
		}),
	},
	types.EmailTypeSubscriptionChanged: {
		Subject: "{{if .plan_changed}}The {{.organization_name}} organization moved {{if .previous_plan}}from {{.previous_plan}} {{end}}to the {{.plan_name}} plan" +
			"{{else if .cancel_scheduled}}The {{.plan_name}} plan for the {{.organization_name}} organization ends {{if .effective_date}}on {{.effective_date}}{{else}}at the end of the period{{end}}" +
			"{{else if .resumed}}The {{.plan_name}} plan for the {{.organization_name}} organization will keep renewing" +
			"{{else}}The {{.plan_name}} plan for the {{.organization_name}} organization has ended{{end}}",
		HTMLTemplate: noticeLayout(notice{
			Title: "Plan update",
			Heading: "{{if .plan_changed}}Plan changed to {{.plan_name}}" + badge(lcBadgeGreen, "Updated") +
				"{{else if .cancel_scheduled}}{{.plan_name}} plan set to end" + badge(lcBadgeAmber, "Ending") +
				"{{else if .resumed}}{{.plan_name}} plan will keep renewing" + badge(lcBadgeGreen, "Renewing") +
				"{{else}}{{.plan_name}} plan ended" + badge(lcBadgeRed, "Ended") + "{{end}}",
			Rows: "{{if .plan_changed}}" + row("New plan", "plan_name") + row("Previous plan", "previous_plan") +
				"{{else}}" + row("Plan", "plan_name") + "{{end}}" +
				"{{if not .ended}}{{if not .cancel_scheduled}}" + row("Billing interval", "interval") + row("Price", "price") + "{{end}}{{end}}" +
				row("Organization", "organization_name") +
				"{{if .cancel_scheduled}}" + row("Ends on", "effective_date") + row("Scheduled by", "changed_by") + row("Scheduled", "changed_at") +
				"{{else if .ended}}" + row("Ended", "changed_at") +
				"{{else}}" + row("Changed by", "changed_by") + row("Changed", "changed_at") + "{{end}}",
			Body: "{{if .plan_changed}}" +
				para(`The <strong>{{.organization_name}}</strong> organization on Everstack moved {{if .previous_plan}}from {{.previous_plan}} {{end}}to the <strong>{{.plan_name}}</strong> plan.`) +
				"{{else if .cancel_scheduled}}" +
				para(`The <strong>{{.plan_name}}</strong> plan for the <strong>{{.organization_name}}</strong> organization on Everstack is set to end {{if .effective_date}}on <strong>{{.effective_date}}</strong>{{else}}at the end of the current billing period{{end}}.`) +
				"{{else if .resumed}}" +
				para(`The scheduled cancellation of the <strong>{{.plan_name}}</strong> plan for the <strong>{{.organization_name}}</strong> organization on Everstack was withdrawn.`) +
				"{{else}}" +
				para(`The <strong>{{.plan_name}}</strong> plan for the <strong>{{.organization_name}}</strong> organization on Everstack has ended.`) +
				"{{end}}",
			Next: "{{if .plan_changed}}" +
				para(`The new limits apply straight away, and any price difference shows on the next invoice.`) +
				"{{else if .cancel_scheduled}}" +
				para(`Everything keeps working until then, and the plan will not renew. After that the organization moves to the {{.fallback_plan}} plan and its limits. Changed your mind? Keep the plan from the billing page before that date.`) +
				"{{else if .resumed}}" +
				para(`The plan renews as before. Nothing else changes.`) +
				"{{else}}" +
				para(`The organization is on the {{.fallback_plan}} plan now and its limits apply. Your data is kept, and you can pick a plan from the billing page at any time.`) +
				"{{end}}",
			Security: "{{if not .ended}}" + securityLine(billingSecurity) + "{{end}}",
			Button:   primaryButton("{{.billing_url}}", "Open billing"),
			Footer:   billingFooter(""),
		}),
	},
}
