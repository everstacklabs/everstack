package templates

import (
	"strings"
	"testing"
)

// Activation tokens are shown only in the signed-in billing page and are
// never sent by email (lifecycle notices tell self-hosted customers so). No
// template may carry one: a token in an inbox is a credential that outlives
// the session that asked for it.
func TestNoTemplateEmailsAnActivationToken(t *testing.T) {
	for emailType, tmpl := range emailTemplates {
		if strings.Contains(string(emailType), "activation") {
			t.Errorf("template %q exists; activation tokens are never emailed", emailType)
		}
		for _, body := range []string{tmpl.Subject, tmpl.HTMLTemplate} {
			if strings.Contains(body, ".activation_token") {
				t.Errorf("template %q renders an activation token", emailType)
			}
		}
	}
}
