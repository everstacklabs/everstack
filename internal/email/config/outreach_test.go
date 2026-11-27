package config

import (
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/email/types"
)

func TestOutreachEnvAndSender(t *testing.T) {
	t.Setenv("EVS_SERVICES_EMAIL_BOOKING_URL", "https://example.com/book")
	t.Setenv("EVS_SERVICES_EMAIL_UNSUBSCRIBE_SECRET", strings.Repeat("s", 32))
	t.Setenv("EVS_SERVICES_EMAIL_UNSUBSCRIBE_SECRET_PREVIOUS", strings.Repeat("p", 32))
	t.Setenv("EVS_SERVICES_EMAIL_UNSUBSCRIBE_URL", "https://app.example.com/email/unsubscribe")
	cfg := DefaultConfig()
	cfg.ApplyOutreachEnv()
	if cfg.BookingLink() != "https://example.com/book" || cfg.UnsubscribeSecret != strings.Repeat("s", 32) || cfg.UnsubscribeSecretPrevious != strings.Repeat("p", 32) || cfg.UnsubscribeURL != "https://app.example.com/email/unsubscribe" {
		t.Fatal("outreach environment was not loaded")
	}
	if cfg.SenderFor(types.EmailTypeFounderNote) != cfg.Senders.Arnab {
		t.Fatal("founder note does not use Arnab")
	}
	cfg.ApplySenderOverrides("Team <team@example.com>", "replies@example.com")
	if cfg.SenderFor(types.EmailTypeFounderNote).FromAddress() != "Team <team@example.com>" {
		t.Fatal("founder sender override was ignored")
	}
	for _, invalid := range []string{"http://example.com/book", "/book", "https://", "javascript:alert(1)"} {
		t.Setenv("EVS_SERVICES_EMAIL_BOOKING_URL", invalid)
		cfg.ApplyOutreachEnv()
		if cfg.BookingURL != "" {
			t.Errorf("unsafe booking URL %q retained", invalid)
		}
	}
}
