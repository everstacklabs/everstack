package service

import (
	"context"
	"testing"
)

type fakeEmailSender struct{}

func (fakeEmailSender) SendMagicLink(context.Context, string, string, string) error { return nil }
func (fakeEmailSender) SendInvitation(context.Context, string, string, string, string, string) error {
	return nil
}

// TestWithEmailRequiresBothSenderAndURL covers the guard on WithEmail. A sender
// with no external URL cannot build a link that resolves, so attaching one
// would turn the log-the-token fallback off and mail out a broken link
// instead. Staying in the fallback is the safer failure.
func TestWithEmailRequiresBothSenderAndURL(t *testing.T) {
	tests := []struct {
		name        string
		sender      EmailSender
		externalURL string
		wantEmail   bool
	}{
		{"both present", fakeEmailSender{}, "https://acme.everstack.ai", true},
		{"nil sender", nil, "https://acme.everstack.ai", false},
		{"empty url", fakeEmailSender{}, "", false},
		{"whitespace url", fakeEmailSender{}, "   ", false},
		{"neither", nil, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := (&SelfHostedAuthService{}).WithEmail(tc.sender, tc.externalURL)
			if got := s.email != nil; got != tc.wantEmail {
				t.Errorf("email attached = %v, want %v", got, tc.wantEmail)
			}
		})
	}
}

// TestWithEmailTrimsTrailingSlash pins the URL normalisation, because links are
// built by concatenation: a trailing slash would produce a double slash in
// every magic-link and invitation URL.
func TestWithEmailTrimsTrailingSlash(t *testing.T) {
	for _, in := range []string{
		"https://acme.everstack.ai/",
		"https://acme.everstack.ai",
		"  https://acme.everstack.ai/  ",
	} {
		s := (&SelfHostedAuthService{}).WithEmail(fakeEmailSender{}, in)
		if s.externalURL != "https://acme.everstack.ai" {
			t.Errorf("WithEmail(%q) externalURL = %q, want %q", in, s.externalURL, "https://acme.everstack.ai")
		}
	}
}
