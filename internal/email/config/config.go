package config

import (
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/email/types"
)

// Config holds all configuration for the email service
type Config struct {
	HTTP     HTTPConfig    `mapstructure:"http" yaml:"http"`
	Resend   ResendConfig  `mapstructure:"resend" yaml:"resend"`
	Senders  SendersConfig `mapstructure:"senders" yaml:"senders"`
	CloudURL string        `mapstructure:"cloud_url" yaml:"cloud_url" env:"CLOUD_URL"`
	DocsURL  string        `mapstructure:"docs_url" yaml:"docs_url" env:"DOCS_URL"`
	LogoURL  string        `mapstructure:"logo_url" yaml:"logo_url" env:"LOGO_URL"`
	// DevRedirectTo, when non-empty, overrides the To address of every outbound
	// email. The original recipient is preserved in the subject line. Intended
	// for dev/staging where you want to exercise the live Resend path without
	// mailing real users or burning monthly quota; set to a Resend test address
	// like "delivered@resend.dev" (which doesn't count against quota).
	DevRedirectTo string `mapstructure:"dev_redirect_to" yaml:"dev_redirect_to"`
}

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Address  string        `mapstructure:"address" yaml:"address"`
	Timeouts TimeoutConfig `mapstructure:"timeouts" yaml:"timeouts"`
}

// TimeoutConfig holds HTTP timeout configuration
type TimeoutConfig struct {
	Read  time.Duration `mapstructure:"read" yaml:"read"`
	Write time.Duration `mapstructure:"write" yaml:"write"`
	Idle  time.Duration `mapstructure:"idle" yaml:"idle"`
}

// ResendConfig holds Resend API configuration
type ResendConfig struct {
	APIKey string `mapstructure:"api_key" yaml:"api_key"`
}

// Sender holds a single From/Reply-To pair.
type Sender struct {
	Name    string `mapstructure:"name" yaml:"name"`
	Email   string `mapstructure:"email" yaml:"email"`
	ReplyTo string `mapstructure:"reply_to" yaml:"reply_to"`
}

// FromAddress returns the RFC 5322 "Name <email>" form, or just the email if no name.
func (s Sender) FromAddress() string {
	if s.Name != "" {
		return s.Name + " <" + s.Email + ">"
	}
	return s.Email
}

// SendersConfig holds the per-purpose sender identities. One verified domain
// in Resend covers all of these; addresses are virtual under everstack.ai.
type SendersConfig struct {
	Accounts Sender `mapstructure:"accounts" yaml:"accounts"` // auth, account lifecycle, MFA, magic links
	Invites  Sender `mapstructure:"invites" yaml:"invites"`   // workspace/org invitations (From name overridden per-send)
	Billing  Sender `mapstructure:"billing" yaml:"billing"`   // receipts, invoices, payment failures
	Licenses Sender `mapstructure:"licenses" yaml:"licenses"` // activation tokens, license events
	Security Sender `mapstructure:"security" yaml:"security"` // new login, API key created, etc.
	Arnab    Sender `mapstructure:"arnab" yaml:"arnab"`       // personal onboarding/welcome
}

// SenderFor returns the configured sender for a given email type.
// Falls back to Accounts if the type is unknown.
func (c *Config) SenderFor(t types.EmailType) Sender {
	switch t {
	case types.EmailTypeInvitation:
		return c.Senders.Invites
	case types.EmailTypeWelcome:
		return c.Senders.Arnab
	case types.EmailTypePaymentSuccess, types.EmailTypePaymentFailed,
		types.EmailTypeUsageWarning, types.EmailTypeUsageExceeded:
		return c.Senders.Billing
	case types.EmailTypeActivationToken:
		return c.Senders.Licenses
	case types.EmailTypeMagicLink, types.EmailTypePasswordReset, types.EmailTypeEmailVerification:
		// All identity-bearing transactional auth mails ship from the Accounts
		// identity so users see a consistent sender across login, reset, and
		// verification flows.
		return c.Senders.Accounts
	default:
		return c.Senders.Accounts
	}
}

// DefaultConfig returns the default configuration with everstack.ai sender identities.
func DefaultConfig() *Config {
	return &Config{
		HTTP: HTTPConfig{
			Address: ":8095",
			Timeouts: TimeoutConfig{
				Read:  15 * time.Second,
				Write: 15 * time.Second,
				Idle:  60 * time.Second,
			},
		},
		Senders: SendersConfig{
			Accounts: Sender{Name: "Everstack", Email: "accounts@everstack.ai", ReplyTo: "support@everstack.ai"},
			Invites:  Sender{Name: "Everstack Team", Email: "invites@everstack.ai", ReplyTo: "support@everstack.ai"},
			Billing:  Sender{Name: "Everstack Billing", Email: "billing@everstack.ai", ReplyTo: "billing@everstack.ai"},
			Licenses: Sender{Name: "Everstack", Email: "licenses@everstack.ai", ReplyTo: "support@everstack.ai"},
			Security: Sender{Name: "Everstack Security", Email: "security@everstack.ai", ReplyTo: "security@everstack.ai"},
			Arnab:    Sender{Name: "Arnab from Everstack", Email: "arnab@everstack.ai", ReplyTo: "arnab@everstack.ai"},
		},
		CloudURL: "https://app.everstack.ai",
		DocsURL:  "https://docs.everstack.ai",
		LogoURL:  "https://everstack.ai/everstack-wordmark-dark.png",
	}
}

// ApplySenderOverrides replaces the From (and optionally Reply-To) on every
// sender identity.
//
// The per-purpose identities below exist so hosted mail arrives from an
// address that suits the message: billing from billing@, invitations from a
// person. That only works for a domain the sending account has verified, so
// any other deployment needs a single address it can actually send as, and
// gets one here rather than losing outbound mail to a provider rejection.
//
// from accepts "Name <address@example.com>" or a bare address. An empty
// argument leaves the configured identities untouched.
func (c *Config) ApplySenderOverrides(from, replyTo string) {
	from = strings.TrimSpace(from)
	replyTo = strings.TrimSpace(replyTo)
	if from == "" && replyTo == "" {
		return
	}

	name, address := "", ""
	if from != "" {
		name, address = parseFrom(from)
	}

	for _, sender := range []*Sender{
		&c.Senders.Accounts,
		&c.Senders.Invites,
		&c.Senders.Billing,
		&c.Senders.Licenses,
		&c.Senders.Security,
		&c.Senders.Arnab,
	} {
		if address != "" {
			sender.Email = address
			// Invitation mail overrides the display name per send to carry the
			// inviter's, so an empty name here is not a loss.
			sender.Name = name
		}
		if replyTo != "" {
			sender.ReplyTo = replyTo
		}
	}
}

// parseFrom splits "Name <address>" into its parts, tolerating a bare address.
func parseFrom(value string) (name, address string) {
	open := strings.LastIndexByte(value, '<')
	close := strings.LastIndexByte(value, '>')
	if open >= 0 && close > open {
		return strings.TrimSpace(value[:open]), strings.TrimSpace(value[open+1 : close])
	}
	return "", value
}
