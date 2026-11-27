package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// Instance limit emails (docs/design/instance-over-limit.md): over the
// plan's limit, the reminder two days before the deadline, and the notice
// naming what was paused. Data keys (service.instanceLimitData):
//
//	organization_name   the organization's display name
//	limit_label         "2 instances"
//	slots_used          instances running now
//	grace_ends_at       the deadline (UTC)
//	extra_cost          "$5.00 per extra instance per month, from prepaid credit"; may be empty
//	kept, paused        paused stage: comma-separated instance names
//	changed_at          when Everstack noticed or paused (UTC)
//	instances_url, billing_url

// instanceRows are the rows every stage shares.
var instanceRows = row("Organization", "organization_name") +
	row("Plan allows", "limit_label") +
	row("Running now", "slots_used")

// instanceKeep is the note about what a paused instance keeps.
const instanceKeep = `Paused instances keep their configuration, API keys, traces and data, and resume from the cloud console once the organization is within its limit.`

// instanceBillingLine points at the billing page, the button's alternative.
const instanceBillingLine = `
    <p style="color: #71717a; font-size: 13px; line-height: 20px; margin: 20px 0 0;">
      To keep every instance instead, <a href="{{.billing_url}}" style="color: #6366f1; text-decoration: none;">upgrade the plan or pay for extra instances</a> on the billing page.
    </p>`

var instanceLimitTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypeInstanceOverLimit: {
		Subject: "The {{.organization_name}} organization is over its Everstack instance limit",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Instance limit",
			Heading: "Over the plan's instance limit" + badge(lcBadgeAmber, "Action needed"),
			Rows: instanceRows +
				row("Deadline", "grace_ends_at") +
				row("Extra instance cost", "extra_cost") +
				row("Noticed", "changed_at"),
			Body: para(`The <strong>{{.organization_name}}</strong> organization on Everstack has <strong>{{.slots_used}}</strong> instances running, and its plan allows {{.limit_label}}.`),
			Next: para(`Everything keeps running until <strong>{{.grace_ends_at}}</strong>. Before then, choose which instances to keep, upgrade the plan, or pay for the extra instances from prepaid credit. If you do nothing, the least recently used instances are paused at that time.`) +
				para(instanceKeep),
			Button: primaryButton("{{.instances_url}}", "Choose which instances to keep"),
			Extra:  instanceBillingLine,
			Footer: billingFooter(""),
		}),
	},
	types.EmailTypeInstanceOverLimitReminder: {
		Subject: "Two days left: the {{.organization_name}} organization is over its Everstack instance limit",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Instance limit reminder",
			Heading: "Two days left to choose which instances to keep" + badge(lcBadgeRed, "Reminder"),
			Rows: instanceRows +
				row("Deadline", "grace_ends_at") +
				row("Extra instance cost", "extra_cost") +
				row("Reminder sent", "changed_at"),
			Body: para(`The <strong>{{.organization_name}}</strong> organization on Everstack still has <strong>{{.slots_used}}</strong> instances running, and its plan allows {{.limit_label}}.`),
			Next: para(`On <strong>{{.grace_ends_at}}</strong> the least recently used instances are paused automatically. Pick the ones to keep now, upgrade the plan, or pay for the extra instances from prepaid credit.`) +
				para(instanceKeep),
			Button: primaryButton("{{.instances_url}}", "Choose which instances to keep"),
			Extra:  instanceBillingLine,
			Footer: billingFooter(""),
		}),
	},
	types.EmailTypeInstancesPaused: {
		Subject: "Everstack paused {{.paused}} for the {{.organization_name}} organization",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Instances paused",
			Heading: "Instances paused" + badge(lcBadgeRed, "Paused"),
			// No "Running now": the count the notice carries may predate the
			// pause; the instance lists say what runs.
			Rows: row("Paused instances", "paused") +
				row("Still running", "kept") +
				row("Organization", "organization_name") +
				row("Plan allows", "limit_label") +
				row("Extra instance cost", "extra_cost") +
				row("Paused", "changed_at"),
			Body: para(`Everstack paused <strong>{{.paused}}</strong> for the <strong>{{.organization_name}}</strong> organization because it was still over its plan's limit of {{.limit_label}} at the deadline.`),
			Next: para(`Requests to a paused instance are refused with a clear error until it is resumed. Swap which instances are active at any time, upgrade the plan, or pay for extra instances from prepaid credit to bring them all back.`) +
				para(instanceKeep),
			Button: primaryButton("{{.instances_url}}", "Choose which instances to keep"),
			Extra:  instanceBillingLine,
			Footer: billingFooter(""),
		}),
	},
}
