package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// Spend limit email (docs/design/spend-limits-v2.md 4.5): estimated spend
// reached 80% or 100% of an organization or API key limit. Data keys (built
// by service.spendLimitData):
//
//	organization_name   the organization's display name
//	is_key, key_name    an API key limit and the key's display name
//	scope_label         "The whole organization" or API key "<name>"
//	scope_phrase        "the <org> organization" or API key "<name>" in it
//	request_scope       "made with this API key" or "from this organization"
//	percent, reached    the threshold, and whether it is the limit itself
//	spent, limit        formatted amounts; limit_label adds the period
//	period_word, period_title, period_noun   daily/Daily/today, monthly/...
//	block               the limit refuses requests when reached
//	enforced            refusals are switched on for the organization; while
//	                    off, a block limit says requests "would be refused"
//	action_label        "Block requests" or "Alert only"
//	resets_at, checked_at   UTC times
//	creator             the recipient created the key (not a billing manager)
//	ended               the window had already reset when the crossing was seen
//	billing_url, logo_url

var spendLimitTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypeSpendLimit: {
		Subject: `{{if .is_key}}API key "{{.key_name}}" in the {{.organization_name}} organization{{else}}The {{.organization_name}} organization{{end}}` +
			`{{if .reached}} reached its {{.period_word}} spend limit{{else}} is at {{.percent}}% of its {{.period_word}} spend limit{{end}}`,
		HTMLTemplate: noticeLayout(notice{
			Title: "Spend limit",
			Heading: "{{if .reached}}{{.period_title}} spend limit reached" +
				"{{if and .block .enforced}}" + badge(lcBadgeRed, "Requests refused") + "{{else}}" + badge(lcBadgeAmber, "100%") + "{{end}}" +
				"{{else}}{{.percent}}% of the {{.period_word}} spend limit used" + badge(lcBadgeAmber, "{{.percent}}%") + "{{end}}",
			Rows: row("Applies to", "scope_label") +
				row("Estimated spend", "spent") +
				row("Limit", "limit_label") +
				row("When reached", "action_label") +
				row("Resets", "resets_at") +
				row("Organization", "organization_name") +
				row("Checked", "checked_at"),
			Body: "{{if .reached}}" +
				para(`Estimated spend for {{.scope_phrase}} reached <strong>{{.spent}}</strong> {{.period_noun}}, which is at or over its <strong>{{.limit}}</strong> {{.period_word}} limit.`) +
				"{{else}}" +
				para(`Estimated spend for {{.scope_phrase}} reached <strong>{{.spent}}</strong> {{.period_noun}}, {{.percent}}% of its <strong>{{.limit}}</strong> {{.period_word}} limit.`) +
				"{{end}}",
			Next: "{{if .ended}}" +
				para(`That {{.period_word}} window ended on {{.resets_at}} and the limit has reset, so spend counts from zero again.`) +
				"{{else if not .block}}" +
				para(`This limit only sends alerts, so requests keep working. It resets on {{.resets_at}}.`) +
				"{{else if and .enforced .reached}}" +
				para(`New requests {{.request_scope}} are refused until the limit resets on {{.resets_at}}. An owner or admin can raise or turn off the limit in Billing.`) +
				"{{else if .enforced}}" +
				para(`When spend reaches the limit, new requests {{.request_scope}} are refused until it resets on {{.resets_at}}.`) +
				"{{else if .reached}}" +
				para(`Requests {{.request_scope}} would be refused now, but spend limits are not enforced for this organization yet, so requests keep working. The limit resets on {{.resets_at}}.`) +
				"{{else}}" +
				para(`When spend reaches the limit, requests {{.request_scope}} would be refused. Spend limits are not enforced for this organization yet, so for now requests keep working. The limit resets on {{.resets_at}}.`) +
				"{{end}}" +
				para(`Spend counts Everstack credit used plus traffic on your own provider keys at list price. It is an estimate: your provider's invoice is authoritative, and models without a list price are not counted.`),
			Button: primaryButton("{{.billing_url}}", "Review spend limits"),
			Footer: footer(`You are receiving this because you {{if .creator}}created API key "{{.key_name}}" in{{else}}manage billing for{{end}} the {{.organization_name}} organization on Everstack. ` +
				`Questions? Reply to this email or write to ` + billingMailto + `.`),
		}),
	},
}
