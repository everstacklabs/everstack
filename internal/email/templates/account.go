package templates

import "github.com/everstacklabs/everstack/internal/email/types"

// Account emails: sign-in link, password reset, verification code, invitation
// and welcome. The auth ones stay short and security-focused: which account,
// when it was asked for, when the link or code stops working, and what to do
// if it wasn't you. They never carry a secret beyond the link or code itself.
// Data keys (service.magicLinkData, passwordResetData, verificationData,
// invitationData, welcomeData):
//
//	account_email       the recipient address the email is about
//	requested_at        when it was asked for (UTC)
//	expires_in          "10 minutes", "24 hours"
//	magic_link, reset_link, code, invite_link
//	organization_name   invitation: the organization's display name; may be empty
//	inviter             invitation: "Jane Smith (jane@acme.com)"; may be empty
//	inviter_name        invitation: the inviter's name, else their address
//	role, expires_at    invitation: "Member", "Oct 11, 2026 at 6:32 PM UTC"
//	user_name           welcome

// accountFooter says why an account email arrived.
func accountFooter(why string) string {
	return footer(`You are receiving this because ` + why + ` Questions? Write to ` + supportMailto + `.`)
}

// authRows are the rows of every sign-in, reset and verification email.
func authRows(expiresLabel string) string {
	return row("Account", "account_email") +
		row("Requested", "requested_at") +
		`{{if .expires_in}}<tr>` + lcLabel + expiresLabel + `</td>` + lcValue + `In {{.expires_in}}</td></tr>{{end}}`
}

var accountTemplates = map[types.EmailType]*EmailTemplate{
	types.EmailTypeMagicLink: {
		Subject: "Your Everstack sign-in link",
		HTMLTemplate: noticeLayout(notice{
			Title:    "Sign in to Everstack",
			Heading:  "Sign in to Everstack",
			Rows:     authRows("Link expires"),
			Body:     para(`Use the button below to sign in to Everstack as <strong>{{.account_email}}</strong>. The link works once.`),
			Security: securityLine(`If you didn't ask to sign in, ignore this email. Nobody can sign in without this link, and it stops working after {{.expires_in}}.`),
			Button:   primaryButton("{{.magic_link}}", "Sign in to Everstack"),
			Extra:    linkFallback("magic_link"),
			Footer:   accountFooter(`someone asked to sign in to Everstack with this email address.`),
		}),
	},
	types.EmailTypePasswordReset: {
		Subject: "Reset your Everstack password",
		HTMLTemplate: noticeLayout(notice{
			Title:    "Reset your password",
			Heading:  "Reset your password",
			Rows:     authRows("Link expires"),
			Body:     para(`Use the button below to choose a new password for the Everstack account <strong>{{.account_email}}</strong>. The link works once.`),
			Security: securityLine(`If you didn't ask to reset your password, ignore this email. Your password stays the same, and the link stops working after {{.expires_in}}.`),
			Button:   primaryButton("{{.reset_link}}", "Reset password"),
			Extra:    linkFallback("reset_link"),
			Footer:   accountFooter(`someone asked to reset the password for this email address on Everstack.`),
		}),
	},
	types.EmailTypeEmailVerification: {
		Subject: "Your Everstack verification code",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Verify your email",
			Heading: "Verify your email",
			Rows:    authRows("Code expires"),
			Body: para(`Enter this code on Everstack to confirm <strong>{{.account_email}}</strong>.`) + `
    <div style="background: #f9fafb; border: 1px solid #e4e4e7; border-radius: 8px; padding: 20px; margin: 0 0 16px; text-align: center;">
      <p style="color: #71717a; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; margin: 0 0 8px; font-weight: 600;">Verification code</p>
      <p style="color: #18181b; font-size: 28px; font-family: 'SF Mono', Monaco, Menlo, monospace; margin: 0; letter-spacing: 0.2em; font-weight: 600;">{{.code}}</p>
    </div>
`,
			Security: securityLine(`If you didn't ask for this code, ignore this email. Nothing changes until the code is entered, and it stops working after {{.expires_in}}. Everstack never asks for this code by phone or chat.`),
			Footer:   accountFooter(`someone entered this email address on Everstack and asked for a code.`),
		}),
	},
	types.EmailTypeInvitation: {
		Subject: "{{.inviter_name}} invited you to {{if .organization_name}}the {{.organization_name}} organization{{else}}their team{{end}} on Everstack",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Invitation to Everstack",
			Heading: "{{if .organization_name}}Join the {{.organization_name}} organization on Everstack{{else}}Join your team on Everstack{{end}}",
			Rows: row("Organization", "organization_name") +
				row("Invited by", "inviter") +
				row("Role", "role") +
				row("Invitation for", "account_email") +
				row("Expires", "expires_at"),
			Body:     para(`<strong>{{.inviter_name}}</strong> invited you to join {{if .organization_name}}the <strong>{{.organization_name}}</strong> organization{{else}}their team{{end}} on Everstack{{if .role}} with the {{.role}} role{{end}}.`),
			Next:     para(`Accept to sign in or create your account, and you land in the organization straight away. The invitation works once{{if .expires_at}} and expires on {{.expires_at}}{{end}}.`),
			Security: securityLine(`If you don't know {{.inviter_name}} or didn't expect this invitation, ignore this email. Nothing happens unless you accept.`),
			Button:   primaryButton("{{.invite_link}}", "Accept invitation"),
			Extra:    linkFallback("invite_link"),
			Footer:   accountFooter(`{{.inviter_name}} invited {{if .account_email}}{{.account_email}}{{else}}this email address{{end}} to Everstack.`),
		}),
	},
	types.EmailTypeWelcome: {
		Subject: "Welcome to Everstack{{if .user_name}}, {{.user_name}}{{end}}",
		HTMLTemplate: noticeLayout(notice{
			Title:   "Welcome to Everstack",
			Heading: "Hi {{if .user_name}}{{.user_name}}{{else}}there{{end}}, welcome to Everstack",
			Body: para(`Thanks for signing up. I'm Arnab, founder of Everstack, and I'm genuinely glad you're here.`) +
				para(`Everstack runs AI agents in isolated, observable sandboxes. Bring your own LLM provider, deploy agents in seconds, and see exactly what they did and what it cost.`) +
				`{{if .starter_credit}}` + para(`Your account includes {{.starter_credit}} of usage credit to try Everstack. It expires 30 days after signup.`) + `{{end}}` + `
    <p style="color: #18181b; font-size: 15px; line-height: 24px; margin: 8px 0 12px; font-weight: 600;">Getting started takes about five minutes</p>
    <table cellpadding="0" cellspacing="0" border="0" style="margin: 0 0 24px; width: 100%;">
      ` + welcomeStep("1", "Connect a provider.", "Add your OpenAI, Anthropic or any compatible API key. Everstack proxies, logs and meters every call.") + `
      ` + welcomeStep("2", "Start your first agent.", "Pick a template or define your own. It runs in its own isolated sandbox, ready in seconds.") + `
      ` + welcomeStep("3", "Bring your team.", "Invite teammates to your organization, share agents, and see usage rolled up in one place.") + `
    </table>
`,
			Button: primaryButton("{{.cloud_url}}", "Open Everstack"),
			Extra: `
    <p style="color: #18181b; font-size: 15px; line-height: 24px; margin: 28px 0 12px;">
      <strong>Stuck on something? Just reply.</strong> This email comes straight to me, and I read and answer every one. The docs are at <a href="{{.docs_url}}" style="color: #6366f1; text-decoration: none;">{{.docs_url}}</a> if you want to dig deeper first.{{if .booking_url}} If you'd rather talk it through, <a href="{{.booking_url}}" style="color: #6366f1; text-decoration: none;">grab 20 minutes with me</a>.{{end}}
    </p>
    <p style="color: #18181b; font-size: 15px; line-height: 24px; margin: 0;">
      Arnab<br>
      <span style="color: #71717a; font-size: 13px;">Founder, Everstack</span>
    </p>`,
			Footer: footer(`You are receiving this because you signed up for Everstack at {{.cloud_url}}{{if .account_email}} as {{.account_email}}{{end}}.`),
		}),
	},
}

// welcomeStep is one numbered row of the welcome email's getting started list.
func welcomeStep(n, title, text string) string {
	return `<tr>
        <td style="vertical-align: top; padding: 0 12px 16px 0; width: 28px;">
          <div style="background: #eef2ff; color: #4338ca; width: 28px; height: 28px; border-radius: 14px; text-align: center; line-height: 28px; font-size: 13px; font-weight: 600;">` + n + `</div>
        </td>
        <td style="vertical-align: top; padding: 0 0 16px 0; color: #18181b; font-size: 15px; line-height: 22px;">
          <strong>` + title + `</strong><br>
          <span style="color: #52525b;">` + text + `</span>
        </td>
      </tr>`
}
