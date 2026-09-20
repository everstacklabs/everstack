package config

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/email/types"
)

// The built-in identities are everstack.ai addresses, which only the hosted
// product can send as. Anyone else must be able to substitute their own, or
// the provider rejects the message for an unverified From domain.
func TestApplySenderOverridesReplacesEveryIdentity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ApplySenderOverrides("Acme <auth@acme.test>", "help@acme.test")

	for _, kind := range []types.EmailType{
		types.EmailTypeMagicLink,
		types.EmailTypeInvitation,
		types.EmailTypeWelcome,
		types.EmailTypePaymentSuccess,
		types.EmailTypeActivationToken,
		types.EmailTypeEmailVerification,
	} {
		sender := cfg.SenderFor(kind)
		if sender.Email != "auth@acme.test" {
			t.Errorf("%s sends from %q, want auth@acme.test", kind, sender.Email)
		}
		if sender.ReplyTo != "help@acme.test" {
			t.Errorf("%s replies to %q, want help@acme.test", kind, sender.ReplyTo)
		}
		if sender.FromAddress() != "Acme <auth@acme.test>" {
			t.Errorf("%s From = %q", kind, sender.FromAddress())
		}
	}
}

func TestApplySenderOverridesAcceptsABareAddress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ApplySenderOverrides("auth@acme.test", "")

	sender := cfg.SenderFor(types.EmailTypeMagicLink)
	if sender.FromAddress() != "auth@acme.test" {
		t.Errorf("From = %q, want the bare address", sender.FromAddress())
	}
	// Reply-To was not overridden, so the default survives rather than being
	// blanked into an unusable empty header.
	if sender.ReplyTo == "" {
		t.Error("Reply-To was cleared by an override that did not mention it")
	}
}

func TestApplySenderOverridesLeavesDefaultsAloneWhenUnset(t *testing.T) {
	cfg := DefaultConfig()
	before := cfg.SenderFor(types.EmailTypeMagicLink)
	cfg.ApplySenderOverrides("", "")

	if got := cfg.SenderFor(types.EmailTypeMagicLink); got != before {
		t.Errorf("identities changed with no override set: %+v", got)
	}
}

func TestApplySenderOverridesCanSetReplyToAlone(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ApplySenderOverrides("", "help@acme.test")

	billing := cfg.SenderFor(types.EmailTypePaymentFailed)
	if billing.ReplyTo != "help@acme.test" {
		t.Errorf("Reply-To = %q, want help@acme.test", billing.ReplyTo)
	}
	if billing.Email != "billing@everstack.ai" {
		t.Errorf("From changed to %q when only Reply-To was overridden", billing.Email)
	}
}
