// Package unsubscribe signs links that authorize only suppressing one person.
// Tokens do not expire because a link in an old email must keep working.
package unsubscribe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

var (
	ErrSecretTooShort = errors.New("unsubscribe secret must have at least 32 characters")
	ErrInvalidToken   = errors.New("invalid unsubscribe token")
)

// Token normalizes the address before signing so storage and verification
// agree even when signup supplied mixed case or surrounding whitespace.
func Token(secret, email, userID string) (string, error) {
	if utf8.RuneCountInString(secret) < 32 {
		return "", ErrSecretTooShort
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > 320 {
		return "", ErrInvalidToken
	}
	b64 := base64.RawURLEncoding
	return "v1." + b64.EncodeToString([]byte(email)) + "." + b64.EncodeToString([]byte(userID)) + "." + b64.EncodeToString(signature(secret, email, userID)), nil
}

func signature(secret, email, userID string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("unsubscribe:v1:" + email + ":" + userID))
	return mac.Sum(nil)
}

// Verify accepts the previous secret as well so rotating the signing key
// does not invalidate links already delivered to inboxes.
func Verify(secrets []string, token string) (email, userID string, err error) {
	configured := false
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if utf8.RuneCountInString(secret) < 32 {
			continue
		}
		configured = true
	}
	if !configured {
		return "", "", ErrSecretTooShort
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", "", ErrInvalidToken
	}
	decoded := make([][]byte, 3)
	for i, part := range parts[1:] {
		value, decodeErr := base64.RawURLEncoding.Strict().DecodeString(part)
		if decodeErr != nil || base64.RawURLEncoding.EncodeToString(value) != part {
			return "", "", ErrInvalidToken
		}
		decoded[i] = value
	}
	email, userID = string(decoded[0]), string(decoded[1])
	if email == "" || len(email) > 320 || email != strings.ToLower(strings.TrimSpace(email)) || len(decoded[2]) != sha256.Size {
		return "", "", ErrInvalidToken
	}
	valid := false
	for _, secret := range secrets {
		if utf8.RuneCountInString(secret) >= 32 {
			// Compare every configured key, including after a match.
			matches := hmac.Equal(signature(secret, email, userID), decoded[2])
			valid = valid || matches
		}
	}
	if !valid {
		return "", "", ErrInvalidToken
	}
	return email, userID, nil
}

// URL preserves existing query parameters and escapes the token as a value.
func URL(base, token string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return "", errors.New("unsubscribe URL must be absolute https (http is allowed on localhost or 127.0.0.1)")
	}
	q := u.Query()
	q.Set("t", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Headers advertises the same endpoint to links and RFC 8058 mail clients.
func Headers(url string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + url + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}
