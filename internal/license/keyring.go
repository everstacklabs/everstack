package license

import (
	"errors"
	"strings"
)

// vendorPublicKeysB64 is a comma-separated list of base64-encoded Ed25519
// public keys, injected at build time into OFFICIAL release binaries:
//
//	go build -ldflags "-X github.com/everstacklabs/everstack/internal/license.vendorPublicKeysB64=<key1>,<key2>"
//
// This is the trust root for OFFLINE license files (air-gapped installs).
// It is deliberately compile-time only — no env var, no config key: a
// runtime-supplied trust root would let an operator substitute their own
// keypair and self-sign an enterprise license
// (docs/design/editions-and-billing.md, section 7). Multiple keys support
// vendor key rotation. An empty value disables offline license loading;
// online activation is unaffected (it delivers the verification key over the
// authenticated activation channel).
var vendorPublicKeysB64 string

// vendorKeyring builds verifiers for every pinned vendor key.
func vendorKeyring() ([]*Verifier, error) {
	raw := strings.TrimSpace(vendorPublicKeysB64)
	if raw == "" {
		return nil, errors.New("no vendor public keys compiled into this binary; offline license files are not supported by this build (activate online instead)")
	}
	var verifiers []*Verifier
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := NewVerifier(part)
		if err != nil {
			return nil, err
		}
		verifiers = append(verifiers, v)
	}
	if len(verifiers) == 0 {
		return nil, errors.New("no valid vendor public keys compiled into this binary")
	}
	return verifiers, nil
}

// VerifyWithVendorKeyring validates a license JWT against the compiled-in
// vendor keys, tolerating an expired exp claim (the caller evaluates expiry
// via the grace state machine). Signature, issuer, and audience are fully
// verified against each pinned key until one matches.
func VerifyWithVendorKeyring(tokenString string) (*Claims, error) {
	verifiers, err := vendorKeyring()
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, v := range verifiers {
		claims, err := v.VerifyAllowExpired(tokenString)
		if err == nil {
			return claims, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// HasVendorKeyring reports whether this binary carries pinned vendor keys
// (i.e. whether offline license files can be used at all).
func HasVendorKeyring() bool {
	return strings.TrimSpace(vendorPublicKeysB64) != ""
}
