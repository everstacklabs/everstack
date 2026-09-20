// Package emailaddr validates email addresses submitted to authentication
// endpoints.
//
// It exists because the auth entry points accepted any string at all. Only
// the invitation path checked, so register, login, magic-link and
// password-reset would happily create a user, mint a token, and hand an
// unroutable address to the mail provider, which then failed somewhere the
// caller could not see. Rejecting at the edge keeps the error next to the
// input that caused it.
//
// Validation is syntactic. It deliberately does not change the case of the
// address: the local part is case-sensitive per RFC 5321, and callers here
// already differ in how they canonicalise, so quietly rewriting the value
// would risk splitting an existing account from its own record.
package emailaddr

import (
	"errors"
	"net/mail"
	"strings"
)

// ErrInvalid reports an address that is not usable. The text is deliberately
// plain, because it is shown to whoever typed it.
var ErrInvalid = errors.New("please enter a valid email address")

// maxLength bounds the whole address. RFC 5321 caps the path at 256 octets
// including the angle brackets, leaving 254 for the address itself.
const maxLength = 254

// Validate reports whether raw is a single, routable-looking address.
//
// It rejects forms that mail.ParseAddress accepts but which have no business
// arriving at a login box: a display name wrapping the address, and a domain
// with no dot, which cannot resolve outside a local network.
func Validate(raw string) error {
	address := strings.TrimSpace(raw)
	if address == "" || len(address) > maxLength {
		return ErrInvalid
	}

	parsed, err := mail.ParseAddress(address)
	if err != nil {
		return ErrInvalid
	}
	// "Someone <a@b.com>" parses, but the caller submitted a form field, not
	// a mail header, so anything other than the bare address is a mistake.
	if parsed.Address != address || parsed.Name != "" {
		return ErrInvalid
	}

	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return ErrInvalid
	}
	domain := address[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return ErrInvalid
	}
	return nil
}
