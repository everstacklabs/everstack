// Package secretlog keeps one-time credentials out of logs.
//
// Verification, password reset, invitation, magic link, session and
// activation tokens are bearer credentials: whoever reads one can use it.
// Logs go to shared infrastructure that more people can read than the
// credential's owner, so they never carry a token or a URL built from one.
// When a log line needs to say which token it is about, it carries
// Fingerprint(token) instead. The guard in secretlog_test.go fails the build
// if a log call names a token or token URL directly.
package secretlog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
)

// fingerprintLength is the number of hex characters kept. Six is enough to
// correlate log lines about the same token and far too few to recover or
// guess it.
const fingerprintLength = 6

// Fingerprint returns a short, non-reversible identifier for a secret: the
// first six hex characters of its SHA-256. It returns "" for an empty secret.
func Fingerprint(secret string) string {
	if secret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])[:fingerprintLength]
}

// credentialInText matches credentials that third-party errors put in their
// messages: WorkOS appends `pending_authentication_token: "..."` to every
// authentication error, and net/http errors quote the request URL, query
// string included.
var credentialInText = []*regexp.Regexp{
	regexp.MustCompile(`(pending_authentication_token"?\s*[:=]\s*)"[^"]*"`),
	regexp.MustCompile(`([?&](?:token|code)=)[^&#\s"']+`),
}

// Redact replaces the credentials credentialInText knows about with
// "[redacted]".
func Redact(text string) string {
	for _, pattern := range credentialInText {
		text = pattern.ReplaceAllString(text, "${1}[redacted]")
	}
	return text
}

// RedactError returns err for logging with Redact applied to its message. It
// returns err itself when nothing was redacted, and nil for nil.
func RedactError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	redacted := Redact(message)
	if redacted == message {
		return err
	}
	return errors.New(redacted)
}
